/*
 * Vartalap messenger client.
 *
 * The server only ever sees ciphertext envelopes. This file owns:
 *   - local identity and contact keys (IndexedDB)
 *   - encryption/decryption and signature checks
 *   - optimistic local history, outbox retries and acknowledgements
 *   - the authenticated WebSocket for real-time delivery, presence and typing
 */
(function (global) {
  'use strict';

  var Crypto = global.VartalapCrypto;
  var DB = global.VartalapDB;
  var UI = global.VartalapUI;

  var MAX_ATTACHMENT_BYTES = 8 * 1024 * 1024;
  var MAX_TEXT = 4000;
  var MESSAGE_DOMAIN = 'vartalap:message:v1';

  var state = {
    self: null,
    me: null,
    socket: null,
    socketOpen: false,
    socketRetry: 1000,
    pingTimer: null,
    conversations: new Map(),
    contacts: new Map(),
    online: new Set(),
    activeId: null,
    activeConversation: null,
    activeContact: null,
    activeMessages: [],
    replyTo: null,
    attachment: null,
    typingTimer: null,
    lastTypingSent: 0,
    peerTypingTimer: null,
    ackQueue: new Set(),
    ackTimer: null,
    sending: false,
    fatal: false
  };

  var elements = {};

  function $(id) {
    return global.document.getElementById(id);
  }

  function el(tag, className, text) {
    var node = global.document.createElement(tag);
    if (className) {
      node.className = className;
    }
    if (text !== undefined && text !== null) {
      node.textContent = text;
    }
    return node;
  }

  function showFatal(message, actionLabel, action) {
    if (state.fatal) {
      return;
    }
    state.fatal = true;
    if (elements.messenger) {
      elements.messenger.classList.add('hidden');
    }
    var box = el('div', 'fatal-state');
    box.appendChild(el('img', 'empty-mark'));
    box.lastChild.src = '/static/logo-mark.svg';
    box.lastChild.alt = '';
    box.appendChild(el('h1', 'empty-title', 'Vartalap cannot start'));
    box.appendChild(el('p', 'empty-copy', message));
    if (actionLabel && action) {
      var button = el('button', 'cds--btn cds--btn--primary', actionLabel);
      button.type = 'button';
      button.addEventListener('click', action);
      box.appendChild(button);
    }
    elements.app.appendChild(box);
  }

  function avatarElement(person, extraClass) {
    var wrapper = el('span', 'avatar' + (extraClass ? ' ' + extraClass : ''));
    if (person && person.avatarUpdatedAt && person.id) {
      var image = el('img', 'avatar-image');
      image.src = '/avatar/' + person.id + '?v=' + person.avatarUpdatedAt;
      image.alt = '';
      wrapper.appendChild(image);
    } else {
      wrapper.textContent = UI.initial(person && (person.displayName || person.username));
    }
    return wrapper;
  }

  function setAvatar(container, person) {
    container.textContent = '';
    container.classList.remove('avatar--image');
    if (person && person.avatarUpdatedAt && person.id) {
      var image = el('img', 'avatar-image');
      image.src = '/avatar/' + person.id + '?v=' + person.avatarUpdatedAt;
      image.alt = '';
      container.appendChild(image);
    } else {
      container.textContent = UI.initial(person && (person.displayName || person.username));
    }
  }

  /* ---- API helpers ------------------------------------------------------ */

  async function fetchConversation(id) {
    return UI.api('/api/conversations/' + encodeURIComponent(id));
  }

  async function updateContactFromBundle(bundle) {
    if (!bundle || !bundle.userId) {
      return null;
    }
    var existing = state.contacts.get(bundle.userId) || await DB.getContact(bundle.userId);
    var fingerprint = await Crypto.fingerprint(bundle.encPublicKey, bundle.sigPublicKey);
    var keysChanged = Boolean(existing && existing.fingerprint && existing.fingerprint !== fingerprint);
    var contact = {
      userId: bundle.userId,
      username: bundle.username,
      displayName: bundle.displayName,
      avatarUpdatedAt: bundle.avatarUpdatedAt || 0,
      deviceId: bundle.deviceId,
      encPublicKey: bundle.encPublicKey,
      sigPublicKey: bundle.sigPublicKey,
      fingerprint: fingerprint,
      verified: keysChanged ? false : Boolean(existing && existing.verified),
      identityChanged: keysChanged || Boolean(existing && existing.identityChanged && !keysChanged),
      updatedAt: Date.now()
    };
    if (keysChanged) {
      contact.previousFingerprint = existing.fingerprint;
      contact.identityChanged = true;
      UI.toast('The identity keys for @' + bundle.username +
        ' changed. Verify the new fingerprint before sharing anything sensitive.', 'error');
    }
    state.contacts.set(contact.userId, contact);
    await DB.saveContact(contact);
    return contact;
  }

  async function createConversation(username) {
    var data = await UI.api('/api/conversations', {
      method: 'POST',
      json: { username: username }
    });
    var conversation = data.conversation;
    await updateContactFromBundle(data.bundle);
    conversation.lastMessageAt = conversation.createdAt;
    conversation.lastPreview = 'New private conversation';
    conversation.unread = 0;
    state.conversations.set(conversation.id, conversation);
    await DB.saveConversation(conversation);
    renderConversations();
    await openConversation(conversation.id);
  }

  /* ---- Conversations ---------------------------------------------------- */

  function renderConversations() {
    if (!elements.conversationList) {
      return;
    }
    var list = Array.from(state.conversations.values()).sort(function (a, b) {
      return (b.lastMessageAt || b.createdAt || 0) - (a.lastMessageAt || a.createdAt || 0);
    });
    elements.conversationList.textContent = '';
    elements.chatCount.textContent = list.length ? String(list.length) : '';
    if (list.length === 0) {
      elements.conversationList.appendChild(
        el('p', 'sidebar-empty', 'No conversations yet. Search for someone to start one.'));
      return;
    }
    list.forEach(function (conversation) {
      var peer = conversation.peer || {};
      var button = el('button', 'conversation-item' +
        (conversation.id === state.activeId ? ' is-active' : ''));
      button.type = 'button';
      button.dataset.conversationId = conversation.id;

      var avatarWrap = el('span', 'conversation-avatar');
      avatarWrap.appendChild(avatarElement(peer, 'avatar--sm'));
      if (state.online.has(peer.id)) {
        avatarWrap.appendChild(el('span', 'online-dot'));
      }
      button.appendChild(avatarWrap);

      var copy = el('span', 'conversation-copy');
      var top = el('span', 'conversation-top');
      top.appendChild(el('span', 'conversation-name', peer.displayName || peer.username || 'Unknown'));
      top.appendChild(el('span', 'conversation-time',
        conversation.lastMessageAt ? UI.formatTime(conversation.lastMessageAt) : ''));
      copy.appendChild(top);
      var preview = el('span', 'conversation-preview');
      var prefix = conversation.lastFrom === 'out' ? 'You: ' : '';
      preview.textContent = prefix + (conversation.lastPreview || 'No messages yet');
      copy.appendChild(preview);
      button.appendChild(copy);

      if (conversation.unread > 0) {
        button.appendChild(el('span', 'unread-badge', String(conversation.unread)));
      }
      elements.conversationList.appendChild(button);
    });
  }

  function closeConversation() {
    state.activeId = null;
    state.activeConversation = null;
    state.activeContact = null;
    state.activeMessages = [];
    state.replyTo = null;
    clearAttachment();
    updateReplyPreview();
    elements.messenger.classList.remove('has-active-chat');
    elements.chatView.classList.add('hidden');
    elements.chatEmpty.classList.remove('hidden');
    elements.typingIndicator.textContent = '';
    renderConversations();
  }

  async function openConversation(id) {
    var conversation = state.conversations.get(id);
    if (!conversation) {
      return;
    }    if (state.activeId !== id) {
      state.replyTo = null;
      clearAttachment();
      updateReplyPreview();
    }
    state.activeId = id;
    state.activeConversation = conversation;
    elements.messenger.classList.add('has-active-chat');

    elements.chatEmpty.classList.add('hidden');
    elements.chatView.classList.remove('hidden');
    elements.chatPeer.href = '/u/' + encodeURIComponent(conversation.peer.username || '');
    elements.chatPeerName.textContent = conversation.peer.displayName || conversation.peer.username;
    elements.chatPeerHandle.textContent = '@' + conversation.peer.username;
    setAvatar(elements.chatPeerAvatar, conversation.peer);
    updatePresence();
    renderConversations();

    try {
      var data = await fetchConversation(id);
      conversation.peer = data.conversation.peer;
      state.activeConversation = conversation;
      await DB.saveConversation(conversation);
      state.activeContact = await updateContactFromBundle(data.bundle);
      elements.chatPeer.href = '/u/' + encodeURIComponent(conversation.peer.username || '');
      elements.chatPeerName.textContent = conversation.peer.displayName || conversation.peer.username;
      elements.chatPeerHandle.textContent = '@' + conversation.peer.username;
      setAvatar(elements.chatPeerAvatar, conversation.peer);
      if (state.activeContact && state.activeContact.identityChanged) {
        UI.toast('This contact’s identity fingerprint changed. Use the shield button to compare it.', 'error');
      }
    } catch (error) {
      UI.toast(error.message || 'Could not load this conversation.', 'error');
      return;
    }

    conversation.unread = 0;
    await DB.saveConversation(conversation);
    renderConversations();

    state.activeMessages = await DB.listMessages(id);
    renderMessages();
    scrollToBottom();
    elements.messageInput.focus();
  }

  /* ---- Messages --------------------------------------------------------- */

  function messageStateLabel(message) {
    if (message.direction !== 'out') {
      return '';
    }
    switch (message.state) {
      case 'pending': return 'sending';
      case 'failed': return 'not sent';
      case 'delivered': return 'delivered';
      default: return 'sent';
    }
  }

  function renderMessages() {
    if (!elements.messageList) {
      return;
    }
    elements.messageList.textContent = '';
    if (state.activeMessages.length === 0) {
      var empty = el('div', 'message-empty');
      empty.appendChild(el('p', 'message-empty-title', 'No messages yet'));
      empty.appendChild(el('p', 'message-empty-copy',
        'Messages are encrypted on this device before they are sent. Say hello.'));
      elements.messageList.appendChild(empty);
      return;
    }
    var previous = null;
    state.activeMessages.forEach(function (message) {
      elements.messageList.appendChild(renderMessage(message, previous));
      previous = message;
    });
  }

  function renderMessage(message, previous) {
    var own = message.direction === 'out';
    var grouped = previous && previous.direction === message.direction &&
      previous.senderUserId === message.senderUserId &&
      Math.abs((message.clientCreatedAt || 0) - (previous.clientCreatedAt || 0)) < 5 * 60 * 1000;
    var row = el('div', 'msg-row' + (own ? ' msg-own' : '') + (grouped ? ' msg-grouped' : ''));

    var avatar = el('span', 'msg-avatar');
    avatar.textContent = UI.initial(message.senderName || (own ? state.me.user.displayName : '?'));
    row.appendChild(avatar);

    var content = el('div', 'msg-content');
    var meta = el('div', 'msg-meta');
    meta.appendChild(el('span', 'msg-author', message.senderName || (own ? state.me.user.displayName : 'Unknown')));
    if (own) {
      meta.appendChild(el('span', 'msg-you', 'you'));
    }
    meta.appendChild(el('span', 'msg-time', UI.formatTime(message.clientCreatedAt || message.serverCreatedAt)));
    var stateLabel = messageStateLabel(message);
    if (stateLabel) {
      meta.appendChild(el('span', 'msg-state msg-state--' + message.state, stateLabel));
    }
    content.appendChild(meta);

    if (message.replyTo) {
      var quote = el('div', 'reply-quote');
      quote.appendChild(el('span', 'reply-quote-author', message.replyTo.author || 'Earlier message'));
      quote.appendChild(el('span', 'reply-quote-text', message.replyTo.preview || ''));
      content.appendChild(quote);
    }

    var bubble = el('div', 'msg-bubble');
    if (message.type === 'attachment' && message.attachment) {
      var card = el('div', 'attachment-card');
      var icon = el('span', 'attachment-icon');
      icon.innerHTML = '<svg width="20" height="20" viewBox="0 0 32 32" fill="currentColor" aria-hidden="true"><path d="M20.6 10.4 12 19a2.1 2.1 0 0 0 3 3l8.6-8.6a4.2 4.2 0 0 0-6-6L8 16a6.3 6.3 0 0 0 9 9l7.6-7.6-1.4-1.4L15.6 24a4.3 4.3 0 0 1-6-6l9.6-9.6a2.2 2.2 0 0 1 3.1 3.1Z"/></svg>';
      card.appendChild(icon);
      var info = el('span', 'attachment-info');
      info.appendChild(el('span', 'attachment-name', message.attachment.name || 'encrypted file'));
      info.appendChild(el('span', 'attachment-size',
        UI.formatBytes(message.attachment.size || 0) + ' · encrypted'));
      card.appendChild(info);
      var download = el('button', 'attachment-download', 'Decrypt');
      download.type = 'button';
      download.dataset.action = 'download';
      download.dataset.messageId = message.id;
      card.appendChild(download);
      bubble.appendChild(card);
      if (message.text) {
        bubble.appendChild(el('div', 'attachment-caption', message.text));
      }
    } else {
      bubble.textContent = message.text || '';
    }
    content.appendChild(bubble);

    var actions = el('div', 'msg-actions');
    var reply = el('button', 'msg-action', 'Reply');
    reply.type = 'button';
    reply.dataset.action = 'reply';
    reply.dataset.messageId = message.id;
    actions.appendChild(reply);
    if (message.state === 'failed') {
      var retry = el('button', 'msg-action', 'Retry');
      retry.type = 'button';
      retry.dataset.action = 'retry';
      retry.dataset.messageId = message.id;
      actions.appendChild(retry);
    }
    var remove = el('button', 'msg-action msg-action--danger', own ? 'Delete' : 'Remove');
    remove.type = 'button';
    remove.dataset.action = 'delete';
    remove.dataset.messageId = message.id;
    actions.appendChild(remove);
    content.appendChild(actions);

    row.appendChild(content);
    return row;
  }

  function scrollToBottom() {
    if (elements.messageList) {
      elements.messageList.scrollTop = elements.messageList.scrollHeight;
    }
  }

  function touchConversation(conversation, message, incoming) {
    conversation.lastMessageAt = message.clientCreatedAt || message.serverCreatedAt || Date.now();
    if (message.type === 'attachment') {
      conversation.lastPreview = 'Attachment · ' + (message.attachment && message.attachment.name || 'file');
    } else {
      conversation.lastPreview = message.text || '';
    }
    conversation.lastFrom = message.direction;
    if (incoming && state.activeId !== conversation.id) {
      conversation.unread = (conversation.unread || 0) + 1;
    }
    DB.saveConversation(conversation).catch(function () {});
  }

  async function storeIncoming(envelope, sender) {
    if (!envelope || !envelope.id || envelope.senderUserId === state.me.user.id) {
      return;
    }
    var existing = await DB.getMessage(envelope.id);
    if (existing) {
      queueAck(envelope.id);
      return;
    }

    var contact = state.contacts.get(envelope.senderUserId);
    if (contact && contact.sigPublicKey && contact.sigPublicKey !== envelope.senderSigningKey) {
      contact.identityChanged = true;
      contact.verified = false;
      await DB.saveContact(contact);
      UI.toast('A message from @' + contact.username + ' used a different signing key. Verify the fingerprint.', 'error');
    }

    var valid = await Crypto.verify(
      envelope.senderSigningKey, envelope.signature, Crypto.canonicalMessage(envelope));
    if (!valid) {
      UI.toast('A message failed signature verification and was not stored.', 'error');
      return;
    }

    var payload;
    try {
      payload = await Crypto.decryptMessage(envelope, state.self.encPrivateKey);
    } catch (error) {
      UI.toast('A message could not be decrypted on this device. It was left on the server.', 'error');
      return;
    }

    var conversation = state.conversations.get(envelope.conversationId);
    if (!conversation) {
      var peer = sender || {
        id: envelope.senderUserId,
        username: 'unknown',
        displayName: 'Unknown'
      };
      conversation = {
        id: envelope.conversationId,
        peer: peer,
        createdAt: envelope.serverCreatedAt || Date.now(),
        lastMessageAt: 0,
        unread: 0
      };
      state.conversations.set(conversation.id, conversation);
      await DB.saveConversation(conversation);
      if (peer.username && peer.username !== 'unknown' && !state.contacts.has(peer.id)) {
        var stub = {
          userId: peer.id,
          username: peer.username,
          displayName: peer.displayName,
          avatarUpdatedAt: peer.avatarUpdatedAt || 0,
          updatedAt: Date.now()
        };
        state.contacts.set(peer.id, stub);
        await DB.saveContact(stub);
      }
    } else if (sender && sender.displayName) {
      conversation.peer = Object.assign({}, conversation.peer, sender);
    }

    var message = {
      id: envelope.id,
      conversationId: envelope.conversationId,
      direction: 'in',
      senderUserId: envelope.senderUserId,
      senderDeviceId: envelope.senderDeviceId,
      recipientUserId: envelope.recipientUserId,
      senderName: (sender && sender.displayName) || conversation.peer.displayName || 'Unknown',
      type: envelope.type,
      text: payload.text || '',
      attachment: payload.attachment || null,
      replyTo: payload.replyTo || null,
      clientCreatedAt: envelope.clientCreatedAt,
      serverCreatedAt: envelope.serverCreatedAt,
      state: 'received'
    };
    await DB.saveMessage(message);
    touchConversation(conversation, message, true);
    renderConversations();

    if (state.activeId === envelope.conversationId) {
      state.activeMessages.push(message);
      renderMessages();
      scrollToBottom();
      conversation.unread = 0;
      await DB.saveConversation(conversation);
      renderConversations();
    } else {
      UI.toast('New message from ' + (message.senderName || 'someone') + '.');
    }
    queueAck(envelope.id);
  }

  function queueAck(messageId) {
    state.ackQueue.add(messageId);
    if (state.ackTimer) {
      return;
    }
    state.ackTimer = global.setTimeout(function () {
      state.ackTimer = null;
      var ids = Array.from(state.ackQueue);
      state.ackQueue.clear();
      if (ids.length === 0) {
        return;
      }
      if (state.socketOpen) {
        sendSocket({ type: 'ack', messageIds: ids });
      } else {
        UI.api('/api/messages/ack', { method: 'POST', json: { messageIds: ids } })
          .catch(function () {
            ids.forEach(function (id) { state.ackQueue.add(id); });
          });
      }
    }, 400);
  }

  async function deliverEnvelope(envelope, silent) {
    try {
      var response = await UI.api(
        '/api/conversations/' + encodeURIComponent(envelope.conversationId) + '/messages',
        { method: 'POST', json: envelope });
      var message = await DB.getMessage(envelope.id);
      if (message) {
        message.state = 'sent';
        message.serverCreatedAt = response.serverCreatedAt;
        await DB.saveMessage(message);
        if (state.activeId === message.conversationId) {
          replaceActiveMessage(message);
        }
      }
      await DB.removeOutbox(envelope.id);
      return true;
    } catch (error) {
      var failed = await DB.getMessage(envelope.id);
      if (failed) {
        failed.state = 'failed';
        await DB.saveMessage(failed);
        if (state.activeId === failed.conversationId) {
          replaceActiveMessage(failed);
        }
      }
      if (!silent) {
        UI.toast(error.message || 'The message could not be sent. It will stay on this device.', 'error');
      }
      return false;
    }
  }

  function replaceActiveMessage(message) {
    for (var i = 0; i < state.activeMessages.length; i += 1) {
      if (state.activeMessages[i].id === message.id) {
        state.activeMessages[i] = message;
        break;
      }
    }
    renderMessages();
  }

  async function ensureContact(conversation) {
    var contact = state.contacts.get(conversation.peer.id);
    if (contact && contact.encPublicKey && contact.sigPublicKey) {
      return contact;
    }
    var data = await fetchConversation(conversation.id);
    conversation.peer = data.conversation.peer;
    await DB.saveConversation(conversation);
    return updateContactFromBundle(data.bundle);
  }

  async function uploadAttachment(ciphertext) {
    return UI.api('/api/conversations/' + encodeURIComponent(state.activeId) + '/attachments', {
      method: 'POST',
      headers: { 'Content-Type': 'application/octet-stream' },
      body: ciphertext
    });
  }

  async function sendCurrent() {
    if (state.sending || !state.activeConversation) {
      return;
    }
    var text = elements.messageInput.value.trim();
    if (!text && !state.attachment) {
      return;
    }
    if (text.length > MAX_TEXT) {
      UI.toast('That message is too long.', 'error');
      return;
    }
    var conversation = state.activeConversation;
    var contact;
    try {
      contact = await ensureContact(conversation);
    } catch (error) {
      UI.toast(error.message || 'Could not load the contact’s public keys.', 'error');
      return;
    }
    if (!contact || !contact.encPublicKey) {
      UI.toast('This contact has no active device key yet.', 'error');
      return;
    }

    state.sending = true;
    elements.sendButton.disabled = true;
    try {
      var type = 'text';
      var payload = { v: 1, kind: 'text', text: text, sentAt: Date.now() };
      if (state.replyTo) {
        payload.replyTo = state.replyTo;
      }

      if (state.attachment) {
        var file = state.attachment;
        if (file.size > MAX_ATTACHMENT_BYTES) {
          throw new Error('Attachments must be 8 MiB or smaller.');
        }
        elements.composerHint.textContent = 'Encrypting attachment…';
        var buffer = new Uint8Array(await file.arrayBuffer());
        var encrypted = await Crypto.encryptAttachment(buffer);
        elements.composerHint.textContent = 'Uploading encrypted attachment…';
        var uploaded = await uploadAttachment(encrypted.ciphertext);
        type = 'attachment';
        payload = {
          v: 1,
          kind: 'attachment',
          text: text,
          sentAt: Date.now(),
          attachment: {
            id: uploaded.id,
            key: encrypted.key,
            iv: encrypted.iv,
            name: file.name || 'attachment',
            mime: file.type || 'application/octet-stream',
            size: file.size
          }
        };
        if (state.replyTo) {
          payload.replyTo = state.replyTo;
        }
      }

      var envelope = await Crypto.encryptMessage({
        conversationId: conversation.id,
        senderUserId: state.me.user.id,
        senderDeviceId: state.me.device.id,
        recipientUserId: contact.userId,
        recipientEncPublicKey: contact.encPublicKey,
        senderSigPublicKey: state.self.sigPublicKey,
        senderSigPrivateKey: state.self.sigPrivateKey,
        type: type,
        payload: payload
      });

      var local = {
        id: envelope.id,
        conversationId: conversation.id,
        direction: 'out',
        senderUserId: state.me.user.id,
        senderDeviceId: state.me.device.id,
        recipientUserId: contact.userId,
        senderName: state.me.user.displayName,
        type: type,
        text: text,
        attachment: payload.attachment || null,
        replyTo: payload.replyTo || null,
        clientCreatedAt: envelope.clientCreatedAt,
        serverCreatedAt: 0,
        state: 'pending'
      };
      await DB.saveMessage(local);
      await DB.addOutbox({ id: envelope.id, conversationId: conversation.id, envelope: envelope });
      state.activeMessages.push(local);
      renderMessages();
      scrollToBottom();
      touchConversation(conversation, local, false);
      renderConversations();

      elements.messageInput.value = '';
      resizeInput();
      updateCharCount();
      clearAttachment();
      state.replyTo = null;
      updateReplyPreview();
      stopTyping();

      await deliverEnvelope(envelope, false);
    } catch (error) {
      UI.toast(error.message || 'Could not send the message.', 'error');
    } finally {
      state.sending = false;
      elements.sendButton.disabled = false;
      elements.composerHint.textContent = 'Encrypted in this browser · Enter to send, Shift+Enter for a new line';
      elements.messageInput.focus();
    }
  }

  async function retryMessage(message) {
    var entry = null;
    var outbox = await DB.listOutbox();
    outbox.forEach(function (candidate) {
      if (candidate.id === message.id) {
        entry = candidate;
      }
    });
    if (!entry) {
      UI.toast('That message can no longer be retried. Send it again.', 'error');
      return;
    }
    message.state = 'pending';
    await DB.saveMessage(message);
    replaceActiveMessage(message);
    await deliverEnvelope(entry.envelope, false);
  }

  async function deleteMessage(message) {
    if (message.direction !== 'out') {
      await DB.deleteMessage(message.id);
      state.activeMessages = state.activeMessages.filter(function (item) { return item.id !== message.id; });
      renderMessages();
      UI.toast('Removed from this device.');
      return;
    }
    try {
      await UI.api('/api/messages/' + encodeURIComponent(message.id), { method: 'DELETE' });
      await DB.deleteMessage(message.id);
      state.activeMessages = state.activeMessages.filter(function (item) { return item.id !== message.id; });
      renderMessages();
      UI.toast('Message deleted before delivery.');
    } catch (error) {
      if (error.status === 409) {
        await DB.deleteMessage(message.id);
        state.activeMessages = state.activeMessages.filter(function (item) { return item.id !== message.id; });
        renderMessages();
        UI.toast('This message was already delivered. It was removed only from this device.');
      } else {
        UI.toast(error.message || 'Could not delete the message.', 'error');
      }
    }
  }

  async function downloadAttachment(message) {
    if (!message.attachment) {
      return;
    }
    try {
      var response = await global.fetch('/api/attachments/' + encodeURIComponent(message.attachment.id), {
        credentials: 'same-origin'
      });
      if (!response.ok) {
        throw new Error('The encrypted attachment is no longer available on the server.');
      }
      var ciphertext = new Uint8Array(await response.arrayBuffer());
      var plaintext = await Crypto.decryptAttachment(
        ciphertext, message.attachment.key, message.attachment.iv);
      var blob = new global.Blob([plaintext], {
        type: message.attachment.mime || 'application/octet-stream'
      });
      var url = global.URL.createObjectURL(blob);
      var link = global.document.createElement('a');
      link.href = url;
      link.download = message.attachment.name || 'attachment';
      global.document.body.appendChild(link);
      link.click();
      global.document.body.removeChild(link);
      global.setTimeout(function () { global.URL.revokeObjectURL(url); }, 1000);
    } catch (error) {
      UI.toast(error.message || 'Could not decrypt the attachment.', 'error');
    }
  }

  /* ---- Reply and attachment UI ----------------------------------------- */

  function setReply(message) {
    var preview = message.type === 'attachment'
      ? 'Attachment · ' + (message.attachment && message.attachment.name || 'file')
      : (message.text || '');
    if (preview.length > 120) {
      preview = preview.slice(0, 119) + '…';
    }
    state.replyTo = {
      id: message.id,
      author: message.senderName || (message.direction === 'out' ? 'You' : 'Contact'),
      preview: preview
    };
    updateReplyPreview();
    elements.messageInput.focus();
  }

  function updateReplyPreview() {
    if (!elements.replyPreview) {
      return;
    }
    if (state.replyTo) {
      elements.replyPreviewText.textContent =
        state.replyTo.author + ': ' + state.replyTo.preview;
      elements.replyPreview.classList.remove('hidden');
    } else {
      elements.replyPreview.classList.add('hidden');
      elements.replyPreviewText.textContent = '';
    }
  }

  function clearAttachment() {
    state.attachment = null;
    if (elements.attachmentPreview) {
      elements.attachmentPreview.classList.add('hidden');
    }
    if (elements.attachmentInput) {
      elements.attachmentInput.value = '';
    }
  }

  function selectAttachment(file) {
    if (!file) {
      clearAttachment();
      return;
    }
    if (file.size > MAX_ATTACHMENT_BYTES) {
      UI.toast('Attachments must be 8 MiB or smaller.', 'error');
      clearAttachment();
      return;
    }
    state.attachment = file;
    elements.attachmentPreviewName.textContent = file.name || 'attachment';
    elements.attachmentPreviewSize.textContent = UI.formatBytes(file.size) + ' · encrypted before upload';
    elements.attachmentPreview.classList.remove('hidden');
  }

  /* ---- Presence and typing --------------------------------------------- */

  function updatePresence() {
    if (!state.activeConversation) {
      return;
    }
    var online = state.online.has(state.activeConversation.peer.id);
    elements.chatPresence.textContent = online ? 'online' : 'offline';
    elements.chatPresence.classList.toggle('is-online', online);
    renderConversations();
  }

  function showPeerTyping() {
    elements.typingIndicator.textContent = 'typing…';
    if (state.peerTypingTimer) {
      global.clearTimeout(state.peerTypingTimer);
    }
    state.peerTypingTimer = global.setTimeout(function () {
      elements.typingIndicator.textContent = '';
    }, 4000);
  }

  function sendTyping(start) {
    if (!state.socketOpen || !state.activeId) {
      return;
    }
    sendSocket({
      type: 'typing',
      conversationId: state.activeId,
      state: start ? 'start' : 'stop'
    });
  }

  function handleTypingInput() {
    if (!state.activeId) {
      return;
    }
    var now = Date.now();
    if (now - state.lastTypingSent > 1800) {
      state.lastTypingSent = now;
      sendTyping(true);
    }
    if (state.typingTimer) {
      global.clearTimeout(state.typingTimer);
    }
    state.typingTimer = global.setTimeout(function () {
      state.typingTimer = null;
      sendTyping(false);
    }, 2600);
  }

  function stopTyping() {
    if (state.typingTimer) {
      global.clearTimeout(state.typingTimer);
      state.typingTimer = null;
    }
    state.lastTypingSent = 0;
    sendTyping(false);
  }

  /* ---- WebSocket -------------------------------------------------------- */

  function sendSocket(event) {
    if (state.socket && state.socket.readyState === global.WebSocket.OPEN) {
      state.socket.send(JSON.stringify(event));
    }
  }

  function setConnection(online) {
    elements.connectionDot.classList.toggle('is-online', online);
    elements.connectionDot.classList.toggle('is-offline', !online);
    elements.connectionDot.title = online ? 'Connected' : 'Reconnecting…';
  }

  function connectSocket() {
    if (state.fatal) {
      return;
    }
    var protocol = global.location.protocol === 'https:' ? 'wss:' : 'ws:';
    var socket;
    try {
      socket = new global.WebSocket(protocol + '//' + global.location.host + '/ws');
    } catch (error) {
      scheduleReconnect();
      return;
    }
    state.socket = socket;

    socket.addEventListener('open', function () {
      state.socketOpen = true;
      state.socketRetry = 1000;
      setConnection(true);
      if (state.pingTimer) {
        global.clearInterval(state.pingTimer);
      }
      state.pingTimer = global.setInterval(function () {
        sendSocket({ type: 'ping' });
      }, 25000);
    });

    socket.addEventListener('message', function (event) {
      var payload;
      try {
        payload = JSON.parse(event.data);
      } catch (error) {
        return;
      }
      handleSocketEvent(payload);
    });

    socket.addEventListener('close', function () {
      state.socketOpen = false;
      setConnection(false);
      if (state.pingTimer) {
        global.clearInterval(state.pingTimer);
        state.pingTimer = null;
      }
      scheduleReconnect();
    });

    socket.addEventListener('error', function () {
      try { socket.close(); } catch (error) { /* ignore */ }
    });
  }

  function scheduleReconnect() {
    if (state.fatal) {
      return;
    }
    var delay = Math.min(state.socketRetry, 15000);
    state.socketRetry = Math.min(state.socketRetry * 1.7, 15000);
    global.setTimeout(connectSocket, delay);
  }

  function handleSocketEvent(event) {
    switch (event.type) {
      case 'ready':
        state.online = new global.Set(event.onlineUserIds || []);
        updatePresence();
        break;
      case 'message':
        storeIncoming(event.envelope, event.sender).catch(function () {});
        break;
      case 'delivery':
        markDelivered(event.messageId).catch(function () {});
        break;
      case 'presence':
        if (event.online) {
          state.online.add(event.userId);
        } else {
          state.online.delete(event.userId);
        }
        updatePresence();
        break;
      case 'typing':
        if (event.conversationId === state.activeId) {
          if (event.state === 'start') {
            showPeerTyping();
          } else {
            elements.typingIndicator.textContent = '';
          }
        }
        break;
      case 'error':
        if (event.message) {
          UI.toast(event.message, 'error');
        }
        break;
      default:
        break;
    }
  }

  async function markDelivered(messageId) {
    var message = await DB.getMessage(messageId);
    if (!message) {
      return;
    }
    message.state = 'delivered';
    await DB.saveMessage(message);
    if (state.activeId === message.conversationId) {
      replaceActiveMessage(message);
    }
  }

  /* ---- Pending and outbox ----------------------------------------------- */

  async function drainPending() {
    var data = await UI.api('/api/messages/pending?limit=200');
    var events = (data && data.messages) || [];
    for (var i = 0; i < events.length; i += 1) {
      await storeIncoming(events[i].envelope, events[i].sender);
    }
  }

  async function retryOutbox() {
    var entries = await DB.listOutbox();
    for (var i = 0; i < entries.length; i += 1) {
      var message = await DB.getMessage(entries[i].id);
      if (message && message.state === 'delivered') {
        await DB.removeOutbox(entries[i].id);
        continue;
      }
      await deliverEnvelope(entries[i].envelope, true);
    }
  }

  /* ---- Settings and verification --------------------------------------- */

  function openSettings() {
    elements.settingsUsername.textContent = '@' + state.me.user.username;
    elements.settingsDevice.textContent = state.me.device.name || 'This device';
    elements.settingsFingerprint.textContent = state.me.fingerprint || state.self.fingerprint;
    elements.settingsProfileLink.href = '/u/' + encodeURIComponent(state.me.user.username);
    if (typeof elements.settingsDialog.showModal === 'function') {
      elements.settingsDialog.showModal();
    }
  }

  function openVerify() {
    if (!state.activeContact) {
      UI.toast('Open a conversation first.', 'error');
      return;
    }
    elements.verifyFingerprint.textContent = state.activeContact.fingerprint || 'Unknown';
    elements.verifyChanged.classList.toggle('hidden', !state.activeContact.identityChanged);
    elements.verifyConfirm.disabled = Boolean(state.activeContact.verified && !state.activeContact.identityChanged);
    if (typeof elements.verifyDialog.showModal === 'function') {
      elements.verifyDialog.showModal();
    }
  }

  async function confirmVerification() {
    if (!state.activeContact) {
      return;
    }
    state.activeContact.verified = true;
    state.activeContact.identityChanged = false;
    await DB.saveContact(state.activeContact);
    UI.toast('Contact marked as verified on this device.');
    elements.verifyDialog.close();
  }

  async function forgetDevice() {
    if (!global.confirm('Forget this device? Your local message history and private keys will be deleted from this browser. You can only come back with your recovery phrase.')) {
      return;
    }
    try {
      await UI.api('/api/forget', { method: 'POST', json: {} });
    } catch (error) {
      UI.toast(error.message || 'Could not revoke this device.', 'error');
      return;
    }
    await DB.clearMessagingData();
    global.location.replace('/');
  }

  /* ---- Event wiring ----------------------------------------------------- */

  function resizeInput() {
    var input = elements.messageInput;
    input.style.height = 'auto';
    input.style.height = Math.min(input.scrollHeight, 148) + 'px';
  }

  function updateCharCount() {
    var length = elements.messageInput.value.length;
    elements.charCount.textContent = length + ' / ' + MAX_TEXT;
    elements.charCount.classList.toggle('is-over', length > MAX_TEXT);
  }

  function bindEvents() {
    elements.conversationList.addEventListener('click', function (event) {
      var button = event.target.closest('[data-conversation-id]');
      if (button) {
        openConversation(button.dataset.conversationId).catch(function (error) {
          UI.toast(error.message || 'Could not open that conversation.', 'error');
        });
      }
    });

    global.document.addEventListener('click', function (event) {
      var starter = event.target.closest('[data-start-chat]');
      if (!starter) {
        return;
      }
      var username = starter.getAttribute('data-start-chat');
      if (!username) {
        return;
      }
      starter.disabled = true;
      createConversation(username).catch(function (error) {
        UI.toast(error.message || 'Could not start that conversation.', 'error');
      }).finally(function () {
        starter.disabled = false;
      });
    });

    elements.messageList.addEventListener('click', function (event) {
      var action = event.target.closest('[data-action]');
      if (!action) {
        return;
      }
      var id = action.dataset.messageId;
      var message = state.activeMessages.filter(function (item) { return item.id === id; })[0];
      if (!message) {
        return;
      }
      if (action.dataset.action === 'reply') {
        setReply(message);
      } else if (action.dataset.action === 'download') {
        downloadAttachment(message);
      } else if (action.dataset.action === 'delete') {
        deleteMessage(message);
      } else if (action.dataset.action === 'retry') {
        retryMessage(message);
      }
    });

    elements.composer.addEventListener('submit', function (event) {
      event.preventDefault();
      sendCurrent();
    });

    elements.messageInput.addEventListener('keydown', function (event) {
      if (event.key === 'Enter' && !event.shiftKey && !event.isComposing) {
        event.preventDefault();
        sendCurrent();
      }
    });

    elements.messageInput.addEventListener('input', function () {
      resizeInput();
      updateCharCount();
      handleTypingInput();
    });

    elements.attachButton.addEventListener('click', function () {
      elements.attachmentInput.click();
    });

    elements.attachmentInput.addEventListener('change', function () {
      selectAttachment(elements.attachmentInput.files && elements.attachmentInput.files[0]);
    });

    elements.attachmentCancel.addEventListener('click', clearAttachment);
    elements.replyCancel.addEventListener('click', function () {
      state.replyTo = null;
      updateReplyPreview();
    });

    elements.settingsOpen.addEventListener('click', openSettings);
    elements.chatBack.addEventListener('click', closeConversation);
    elements.settingsClose.addEventListener('click', function () { elements.settingsDialog.close(); });
    elements.forgetDevice.addEventListener('click', forgetDevice);
    elements.chatVerify.addEventListener('click', openVerify);
    elements.verifyClose.addEventListener('click', function () { elements.verifyDialog.close(); });
    elements.verifyDone.addEventListener('click', function () { elements.verifyDialog.close(); });
    elements.verifyConfirm.addEventListener('click', confirmVerification);
  }

  function cacheElements() {
    elements.app = $('app');
    elements.messenger = $('messenger');
    elements.selfAvatar = $('self-avatar');
    elements.selfName = $('self-name');
    elements.selfHandle = $('self-handle');
    elements.selfFingerprint = $('self-fingerprint');
    elements.connectionDot = $('connection-dot');
    elements.conversationList = $('conversation-list');
    elements.chatCount = $('chat-count');
    elements.chatEmpty = $('chat-empty');
    elements.chatView = $('chat-view');
    elements.chatPeer = $('chat-peer');
    elements.chatBack = $('chat-back');
    elements.chatPeerAvatar = $('chat-peer-avatar');
    elements.chatPeerName = $('chat-peer-name');
    elements.chatPeerHandle = $('chat-peer-handle');
    elements.chatPresence = $('chat-presence');
    elements.messageList = $('message-list');
    elements.typingIndicator = $('typing-indicator');
    elements.composer = $('composer');
    elements.messageInput = $('message-input');
    elements.sendButton = $('send-button');
    elements.attachButton = $('attach-button');
    elements.attachmentInput = $('attachment-input');
    elements.attachmentPreview = $('attachment-preview');
    elements.attachmentPreviewName = $('attachment-preview-name');
    elements.attachmentPreviewSize = $('attachment-preview-size');
    elements.attachmentCancel = $('attachment-cancel');
    elements.replyPreview = $('reply-preview');
    elements.replyPreviewText = $('reply-preview-text');
    elements.replyCancel = $('reply-cancel');
    elements.composerHint = $('composer-hint');
    elements.charCount = $('char-count');
    elements.settingsOpen = $('settings-open');
    elements.settingsDialog = $('settings-dialog');
    elements.settingsClose = $('settings-close');
    elements.settingsUsername = $('settings-username');
    elements.settingsDevice = $('settings-device');
    elements.settingsFingerprint = $('settings-fingerprint');
    elements.settingsProfileLink = $('settings-profile-link');
    elements.forgetDevice = $('forget-device');
    elements.chatVerify = $('chat-verify');
    elements.verifyDialog = $('verify-dialog');
    elements.verifyClose = $('verify-close');
    elements.verifyDone = $('verify-done');
    elements.verifyConfirm = $('verify-confirm');
    elements.verifyFingerprint = $('verify-fingerprint');
    elements.verifyChanged = $('verify-changed');
  }

  /* ---- Boot ------------------------------------------------------------- */

  async function boot() {
    cacheElements();
    UI.bindTheme('#theme-toggle');

    try {
      Crypto.ensureCrypto();
    } catch (error) {
      showFatal(error.message);
      return;
    }

    var identity = await DB.getIdentity();
    if (!identity || !identity.userId) {
      showFatal('This browser has no private keys for Vartalap. Restore your identity with your recovery phrase.',
        'Go to recovery', function () { global.location.replace('/'); });
      return;
    }
    state.self = identity;

    var me;
    try {
      me = await UI.api('/api/me');
    } catch (error) {
      if (error.status === 401) {
        showFatal('This browser is no longer signed in. If you have your recovery phrase, restore your identity.',
          'Restore identity', async function () {
            await DB.clearMessagingData();
            global.location.replace('/');
          });
      } else {
        showFatal(error.message || 'Could not reach the server.');
      }
      return;
    }

    if (me.user.id !== identity.userId) {
      showFatal('This browser is signed into a different Vartalap session than the keys stored here. ' +
        'Restore your identity with your recovery phrase to use this device again.',
      'Restore identity', async function () {
        await DB.clearMessagingData();
        global.location.replace('/');
      });
      return;
    }

    state.me = me;
    state.self.username = me.user.username;
    state.self.displayName = me.user.displayName;
    state.self.fingerprint = me.fingerprint || identity.fingerprint;
    await DB.saveIdentity(state.self);

    elements.selfName.textContent = me.user.displayName;
    elements.selfHandle.textContent = '@' + me.user.username;
    elements.selfFingerprint.textContent = 'fingerprint ' + (me.fingerprint || identity.fingerprint).split(' ').slice(0, 3).join(' ');
    setAvatar(elements.selfAvatar, me.user);

    var localConversations = await DB.listConversations();
    localConversations.forEach(function (conversation) {
      state.conversations.set(conversation.id, conversation);
    });
    var contacts = await DB.listContacts();
    contacts.forEach(function (contact) {
      state.contacts.set(contact.userId, contact);
    });

    try {
      var data = await UI.api('/api/conversations');
      (data.conversations || []).forEach(function (conversation) {
        var local = state.conversations.get(conversation.id);
        conversation.lastMessageAt = local ? local.lastMessageAt : conversation.createdAt;
        conversation.lastPreview = local ? local.lastPreview : 'New private conversation';
        conversation.lastFrom = local ? local.lastFrom : '';
        conversation.unread = local ? local.unread : 0;
        state.conversations.set(conversation.id, conversation);
        DB.saveConversation(conversation).catch(function () {});
      });
    } catch (error) {
      UI.toast(error.message || 'Could not load conversations.', 'error');
    }
    renderConversations();
    bindEvents();

    connectSocket();
    drainPending().catch(function () {});
    retryOutbox().catch(function () {});

    var params = new global.URLSearchParams(global.location.search);
    var chat = params.get('chat');
    if (chat) {
      global.history.replaceState({}, '', '/');
      createConversation(chat).catch(function (error) {
        UI.toast(error.message || 'Could not start that conversation.', 'error');
      });
    }

    global.addEventListener('online', function () {
      if (!state.socketOpen) {
        connectSocket();
      }
      drainPending().catch(function () {});
      retryOutbox().catch(function () {});
    });

    global.document.addEventListener('visibilitychange', function () {
      if (!global.document.hidden) {
        drainPending().catch(function () {});
      }
    });
  }

  if (global.document.readyState === 'loading') {
    global.document.addEventListener('DOMContentLoaded', boot);
  } else {
    boot();
  }
})(window);
