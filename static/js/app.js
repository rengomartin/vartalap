/* Client glue: htmx owns the websocket and server-rendered fragments; this file
   handles the composer, optimistic messages, scrolling, theme choice and the
   persistent device identity (cookie + localStorage recovery). */
(function () {
  'use strict';

  var shortTime = new Intl.DateTimeFormat(undefined, { hour: '2-digit', minute: '2-digit' });
  var longTime = new Intl.DateTimeFormat(undefined, { dateStyle: 'medium', timeStyle: 'short' });
  var historyScroll = null;
  var socketWrapper = null;
  var sendQueue = [];
  var pending = [];

  var DEVICE_KEY = 'vartalap.device.v1';
  var DEVICE_SYNC_COOKIE = 'vartalap_sync';
  var RESTORE_KEY = 'vartalap.restore-attempted';
  var THEME_KEY = 'vartalap.theme';
  var COOKIE_DAYS = 400;

  var SEND_ERRORS = [
    'could not be read', 'cannot be empty', 'at most', 'slow down',
    'could not be saved', 'session has expired'
  ];

  function byId(id) { return document.getElementById(id); }

  function newNonce() {
    var bytes = new Uint8Array(12);
    if (window.crypto && window.crypto.getRandomValues) window.crypto.getRandomValues(bytes);
    else for (var i = 0; i < bytes.length; i++) bytes[i] = Math.floor(Math.random() * 256);
    return Array.prototype.map.call(bytes, function (b) {
      return ('0' + b.toString(16)).slice(-2);
    }).join('');
  }

  /* Device identity ------------------------------------------------------ */

  function readCookie(name) {
    var prefix = name + '=';
    var parts = document.cookie ? document.cookie.split('; ') : [];
    for (var i = 0; i < parts.length; i++) {
      if (parts[i].indexOf(prefix) === 0) {
        try { return decodeURIComponent(parts[i].slice(prefix.length)); }
        catch (error) { return parts[i].slice(prefix.length); }
      }
    }
    return '';
  }

  function writeCookie(name, value, days) {
    var secure = location.protocol === 'https:' ? ';secure' : '';
    document.cookie = name + '=' + encodeURIComponent(value) +
      ';path=/;max-age=' + (days * 86400) + ';samesite=lax' + secure;
  }

  function readStoredDevice() {
    try { return localStorage.getItem(DEVICE_KEY) || ''; } catch (error) { return ''; }
  }

  function storeDevice(token) {
    if (!token) return;
    try { localStorage.setItem(DEVICE_KEY, token); } catch (error) { /* storage may be blocked */ }
  }

  function forgetDevice() {
    try { localStorage.removeItem(DEVICE_KEY); } catch (error) { /* ignore */ }
    try { sessionStorage.removeItem(RESTORE_KEY); } catch (error) { /* ignore */ }
    writeCookie(DEVICE_SYNC_COOKIE, '', 0);
  }

  function setupIdentity() {
    var syncToken = readCookie(DEVICE_SYNC_COOKIE);
    var storedToken = readStoredDevice();
    var inChat = !!byId('chat-view');

    if (inChat) {
      // The server verified us. Keep the localStorage mirror aligned with the
      // token the server actually accepted.
      var accepted = syncToken || storedToken;
      if (accepted) {
        storeDevice(accepted);
        if (!syncToken) writeCookie(DEVICE_SYNC_COOKIE, accepted, COOKIE_DAYS);
      }
      try { sessionStorage.removeItem(RESTORE_KEY); } catch (error) { /* ignore */ }
      return;
    }

    // The server only renders the welcome screen when it has no valid identity.
    // If this browser kept a signed token in localStorage, hand it back once
    // and let the server re-issue the HttpOnly cookie. Note that the server
    // may have just minted a fresh token for this request; prefer the stored
    // one until the restore attempt has finished.
    if (storedToken) {
      // The server has already accepted this exact token, so a restore POST
      // cannot add anything (for example, the visitor never chose a name).
      if (storedToken === syncToken) return;

      var attempted = false;
      try { attempted = !!sessionStorage.getItem(RESTORE_KEY); } catch (error) { /* ignore */ }

      if (attempted) {
        // The restore round-trip came back to the welcome screen, so the old
        // token is not usable. Adopt the server's fresh identity instead.
        try { localStorage.removeItem(DEVICE_KEY); } catch (error) { /* ignore */ }
        if (syncToken) storeDevice(syncToken);
        return;
      }

      if (!syncToken || syncToken !== storedToken) {
        writeCookie(DEVICE_SYNC_COOKIE, storedToken, COOKIE_DAYS);
      }
      try { sessionStorage.setItem(RESTORE_KEY, '1'); } catch (error) { return; }

      var form = document.createElement('form');
      form.method = 'POST';
      form.action = '/restore';
      form.hidden = true;
      var input = document.createElement('input');
      input.type = 'hidden';
      input.name = 'token';
      input.value = storedToken;
      form.appendChild(input);
      document.body.appendChild(form);
      form.submit();
      return;
    }

    // First visit: remember the identity the server just issued.
    if (syncToken) storeDevice(syncToken);
  }

  /* Theme ---------------------------------------------------------------- */

  function isDarkTheme() {
    return document.documentElement.classList.contains('cds--g100');
  }

  function syncThemeIcons() {
    var lightIcon = byId('theme-icon-light');
    var darkIcon = byId('theme-icon-dark');
    var dark = isDarkTheme();
    if (lightIcon) lightIcon.classList.toggle('hidden', !dark);
    if (darkIcon) darkIcon.classList.toggle('hidden', dark);
  }

  function setTheme(dark) {
    var root = document.documentElement;
    root.classList.remove('cds--white', 'cds--g100');
    root.classList.add(dark ? 'cds--g100' : 'cds--white');
    root.style.colorScheme = dark ? 'dark' : 'light';
    try { localStorage.setItem(THEME_KEY, dark ? 'dark' : 'light'); } catch (error) { /* ignore */ }
    syncThemeIcons();
  }

  function setupTheme() {
    syncThemeIcons();
    var toggle = byId('theme-toggle');
    if (!toggle || toggle.dataset.ready) return;
    toggle.dataset.ready = '1';
    toggle.addEventListener('click', function () { setTheme(!isDarkTheme()); });
  }

  /* Transcript helpers ---------------------------------------------------- */

  function formatTimes(root) {
    root.querySelectorAll('time[data-ts]').forEach(function (el) {
      var date = new Date(Number(el.getAttribute('data-ts')));
      if (isNaN(date.getTime())) return;
      el.textContent = shortTime.format(date);
      el.title = longTime.format(date);
    });
  }

  function nearBottom(list) {
    return list.scrollHeight - list.scrollTop - list.clientHeight < 120;
  }

  function scrollDown(list) {
    requestAnimationFrame(function () { list.scrollTop = list.scrollHeight; });
  }

  function setConnection(state) {
    var dot = byId('connection-dot');
    var label = byId('connection-status');
    if (!dot || !label) return;
    dot.classList.toggle('is-online', state === 'online');
    dot.classList.toggle('is-offline', state !== 'online');
    label.textContent = state === 'online' ? 'Connected' :
      state === 'connecting' ? 'Connecting…' : 'Reconnecting…';
  }

  function setSending(active) {
    var button = byId('send-button');
    if (!button) return;
    var input = byId('message-input');
    if (active) button.dataset.sending = '1';
    else delete button.dataset.sending;
    button.disabled = active || !input || input.value.trim() === '';
    var sendIcon = byId('send-icon');
    var spinner = byId('spinner-icon');
    if (sendIcon) sendIcon.classList.toggle('hidden', active);
    if (spinner) spinner.classList.toggle('hidden', !active);
  }

  function updateComposer() {
    var input = byId('message-input');
    var button = byId('send-button');
    var count = byId('char-count');
    if (!input) return;
    if (count) count.textContent = input.value.length + '/' + input.maxLength;
    if (button && !button.dataset.sending) button.disabled = input.value.trim() === '';
    input.style.height = 'auto';
    input.style.height = Math.min(input.scrollHeight, 148) + 'px';
  }

  function resetComposer() {
    var input = byId('message-input');
    if (input) input.value = '';
    setSending(false);
    updateComposer();
    if (input && window.matchMedia('(pointer: fine)').matches) input.focus();
  }

  function currentName() {
    var label = byId('name-display');
    return label ? label.textContent.trim() || 'You' : 'You';
  }

  function initialOf(name) {
    var text = (name || '').trim();
    return text ? Array.from(text)[0].toUpperCase() : '?';
  }

  function buildPendingRow(text, token) {
    var row = document.createElement('div');
    row.className = 'msg-row msg-own is-pending msg-enter';
    row.setAttribute('data-sender', '');
    row.setAttribute('data-id', '');
    row.setAttribute('data-nonce', token);
    row.setAttribute('data-mine', 'true');

    var avatar = document.createElement('span');
    avatar.className = 'msg-avatar';
    avatar.setAttribute('aria-hidden', 'true');
    avatar.textContent = initialOf(currentName());
    row.appendChild(avatar);

    var content = document.createElement('div');
    content.className = 'msg-content';

    var meta = document.createElement('div');
    meta.className = 'msg-meta';
    var author = document.createElement('span');
    author.className = 'msg-author';
    author.textContent = currentName();
    var you = document.createElement('span');
    you.className = 'msg-you';
    you.textContent = 'you';
    var time = document.createElement('time');
    time.className = 'msg-time';
    time.textContent = shortTime.format(new Date());
    meta.appendChild(author);
    meta.appendChild(you);
    meta.appendChild(time);

    var bubble = document.createElement('div');
    bubble.className = 'msg-bubble';
    bubble.textContent = text;

    content.appendChild(meta);
    content.appendChild(bubble);
    row.appendChild(content);
    return row;
  }

  function appendPending(text) {
    var list = byId('message-list');
    if (!list) return null;
    var empty = byId('empty-state');
    if (empty) empty.remove();

    var token = newNonce();
    var row = buildPendingRow(text, token);
    list.appendChild(row);
    pending.push({ nonce: token, text: text, row: row });
    scrollDown(list);
    return token;
  }

  function updateGrouping(row) {
    var previous = row.previousElementSibling;
    if (previous && previous.classList && previous.classList.contains('msg-row') &&
        previous.getAttribute('data-sender') === row.getAttribute('data-sender')) {
      row.classList.add('msg-grouped');
    }
  }

  function confirmPending(row) {
    var token = row.getAttribute('data-nonce');
    if (!token || !row.getAttribute('data-id')) return;
    var removed = false;
    for (var i = pending.length - 1; i >= 0; i--) {
      if (pending[i].nonce !== token) continue;
      if (pending[i].row && pending[i].row.parentNode) pending[i].row.remove();
      pending.splice(i, 1);
      removed = true;
    }
    if (removed) updateGrouping(row);
  }

  function dropPending(restore) {
    if (!pending.length) return;
    var item = pending.shift();
    if (item.row && item.row.parentNode) item.row.remove();
    var input = byId('message-input');
    if (restore && input && !input.value.trim()) {
      input.value = item.text;
      updateComposer();
    }
  }

  function sendFrame(frame) {
    if (socketWrapper) {
      socketWrapper.send(frame);
      return;
    }
    sendQueue.push(frame);
  }

  function flushQueue() {
    if (!socketWrapper) return;
    while (sendQueue.length) socketWrapper.send(sendQueue.shift());
  }

  function handleAddedMessages(nodes) {
    var list = byId('message-list');
    if (!list) return;
    var shouldScroll = false;

    nodes.forEach(function (node) {
      if (!node.element || node.element.nodeType !== 1) return;
      var row = node.element.classList && node.element.classList.contains('msg-row') ? node.element : null;
      if (!row) {
        node.element.querySelectorAll('.msg-row').forEach(function (child) {
          formatTimes(child);
          child.classList.add('msg-enter');
          confirmPending(child);
        });
        return;
      }
      formatTimes(row);
      if (!row.classList.contains('is-pending')) row.classList.add('msg-enter');
      updateGrouping(row);
      var empty = byId('empty-state');
      if (empty) empty.remove();
      confirmPending(row);

      var isLive = node.nextSibling === null ||
        (node.nextSibling.nodeType === 1 && node.nextSibling.id === 'composer-meta');
      if (isLive && (row.getAttribute('data-mine') === 'true' || nearBottom(list))) shouldScroll = true;
    });

    if (shouldScroll) scrollDown(list);
  }

  function setupMessages() {
    var list = byId('message-list');
    if (!list || list.dataset.observed) return;
    list.dataset.observed = '1';
    formatTimes(list);
    list.scrollTop = list.scrollHeight;

    new MutationObserver(function (records) {
      var nodes = [];
      records.forEach(function (record) {
        Array.prototype.forEach.call(record.addedNodes, function (element) {
          nodes.push({ element: element, nextSibling: record.nextSibling });
        });
      });
      if (nodes.length) handleAddedMessages(nodes);
    }).observe(list, { childList: true });

    list.addEventListener('scroll', function () {
      if (list.scrollTop > 48) return;
      var sentinel = byId('load-more');
      if (sentinel && sentinel.hasAttribute('hx-get') && !sentinel.dataset.requested) {
        sentinel.dataset.requested = '1';
        sentinel.dispatchEvent(new Event('load-older'));
      }
    });
  }

  function setupComposer() {
    var input = byId('message-input');
    var button = byId('send-button');
    var form = byId('composer');
    if (!input || !button || !form || form.dataset.ready) return;
    form.dataset.ready = '1';
    updateComposer();
    if (window.matchMedia('(pointer: fine)').matches) input.focus();

    input.addEventListener('input', updateComposer);
    input.addEventListener('keydown', function (event) {
      if (event.key === 'Enter' && !event.shiftKey && !event.isComposing) {
        event.preventDefault();
        if (input.value.trim()) form.requestSubmit();
      }
    });

    form.addEventListener('submit', function (event) {
      event.preventDefault();
      var text = input.value.trim();
      if (!text) return;
      if (input.maxLength > 0 && text.length > input.maxLength) text = text.slice(0, input.maxLength);

      var token = appendPending(text);
      if (!token) return;
      sendFrame(JSON.stringify({ content: text, nonce: token }));
      resetComposer();
    });
  }

  function openDialog(dialog) {
    if (!dialog) return;
    if (typeof dialog.showModal === 'function') dialog.showModal();
    else dialog.setAttribute('open', '');
  }

  function closeDialog(dialog) {
    if (dialog) dialog.close();
  }

  function setupNameDialog() {
    var dialog = byId('name-dialog');
    var open = byId('edit-name');
    var cancel = byId('cancel-name');
    var cancelFooter = byId('cancel-name-footer');
    if (!dialog || !open || dialog.dataset.ready) return;
    dialog.dataset.ready = '1';
    open.addEventListener('click', function () {
      openDialog(dialog);
      var field = byId('new_display_name');
      if (field) field.focus();
    });
    if (cancel) cancel.addEventListener('click', function () { closeDialog(dialog); });
    if (cancelFooter) cancelFooter.addEventListener('click', function () { closeDialog(dialog); });
    dialog.addEventListener('click', function (event) {
      if (event.target === dialog) closeDialog(dialog);
    });
  }

  function setupDeviceDialog() {
    var dialog = byId('device-dialog');
    var open = byId('device-button');
    var close = byId('close-device');
    var closeFooter = byId('close-device-footer');
    if (!dialog || !open || dialog.dataset.ready) return;
    dialog.dataset.ready = '1';
    open.addEventListener('click', function () {
      var protocol = byId('device-protocol');
      if (protocol) {
        protocol.textContent = location.protocol === 'https:' ? 'Secure · HTTPS' : 'Local · HTTP';
      }
      openDialog(dialog);
    });
    if (close) close.addEventListener('click', function () { closeDialog(dialog); });
    if (closeFooter) closeFooter.addEventListener('click', function () { closeDialog(dialog); });
    dialog.addEventListener('click', function (event) {
      if (event.target === dialog) closeDialog(dialog);
    });
  }

  function setupForget() {
    var form = byId('forget-form');
    if (!form || form.dataset.ready) return;
    form.dataset.ready = '1';
    form.addEventListener('submit', forgetDevice);
  }

  function setupChat() {
    if (!byId('chat-view')) return;
    setupMessages();
    setupComposer();
    setupNameDialog();
    setupDeviceDialog();
    setupForget();
  }

  document.addEventListener('DOMContentLoaded', function () {
    setupIdentity();
    setupTheme();
    setupChat();
  });
  document.addEventListener('htmx:afterSwap', function () {
    setupTheme();
    setupChat();
  });

  document.addEventListener('htmx:wsConnecting', function () { setConnection('connecting'); });
  document.addEventListener('htmx:wsOpen', function (event) {
    socketWrapper = event.detail && event.detail.socketWrapper;
    setConnection('online');
    flushQueue();
  });
  document.addEventListener('htmx:wsError', function () {
    socketWrapper = null;
    setConnection('offline');
  });
  document.addEventListener('htmx:wsClose', function (event) {
    socketWrapper = null;
    if (event.detail && event.detail.event && event.detail.event.code === 1000) return;
    setConnection('offline');
  });

  document.addEventListener('htmx:afterRequest', function (event) {
    var path = event.detail && event.detail.requestConfig && event.detail.requestConfig.path;
    if (path === '/name' && event.detail.successful) {
      var dialog = byId('name-dialog');
      if (dialog) closeDialog(dialog);
    }
  });

  document.addEventListener('htmx:beforeSwap', function (event) {
    var path = event.detail && event.detail.requestConfig && event.detail.requestConfig.path;
    var list = byId('message-list');
    if (path && path.indexOf('/history') === 0 && list) {
      historyScroll = { height: list.scrollHeight, top: list.scrollTop };
    }
  });

  document.addEventListener('htmx:afterSwap', function (event) {
    var path = event.detail && event.detail.requestConfig && event.detail.requestConfig.path;
    var list = byId('message-list');
    if (path && path.indexOf('/history') === 0 && list && historyScroll) {
      list.scrollTop = historyScroll.top + (list.scrollHeight - historyScroll.height);
      historyScroll = null;
    }
  });

  new MutationObserver(function (records) {
    records.forEach(function (record) {
      Array.prototype.forEach.call(record.addedNodes, function (node) {
        if (node.nodeType !== 1 || !node.classList.contains('toast')) return;
        var text = (node.textContent || '').toLowerCase();
        if (node.classList.contains('toast--error') &&
            SEND_ERRORS.some(function (phrase) { return text.indexOf(phrase) !== -1; })) {
          dropPending(true);
        }
        setTimeout(function () { node.remove(); }, 4000);
      });
    });
  }).observe(document.documentElement, { childList: true, subtree: true });
})();
