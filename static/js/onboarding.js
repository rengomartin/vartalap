/* Onboarding: create a local cryptographic identity or restore one. */
(function (global) {
  'use strict';

  var Crypto = global.VartalapCrypto;
  var DB = global.VartalapDB;
  var UI = global.VartalapUI;

  var phraseState = { phrase: '', username: '', userId: '', backup: null };

  function status(element, message, isError) {
    element.textContent = message;
    element.classList.toggle('is-error', Boolean(isError));
  }

  function bindTabs() {
    var createTab = global.document.getElementById('tab-create');
    var restoreTab = global.document.getElementById('tab-restore');
    var createPane = global.document.getElementById('pane-create');
    var restorePane = global.document.getElementById('pane-restore');

    function select(which) {
      var creating = which === 'create';
      createTab.classList.toggle('is-active', creating);
      restoreTab.classList.toggle('is-active', !creating);
      createTab.setAttribute('aria-selected', String(creating));
      restoreTab.setAttribute('aria-selected', String(!creating));
      createPane.classList.toggle('hidden', !creating);
      restorePane.classList.toggle('hidden', creating);
    }

    createTab.addEventListener('click', function () { select('create'); });
    restoreTab.addEventListener('click', function () { select('restore'); });
  }

  function bindPhraseDialog() {
    var dialog = global.document.getElementById('phrase-dialog');
    var grid = global.document.getElementById('phrase-grid');
    var copy = global.document.getElementById('phrase-copy');
    var download = global.document.getElementById('phrase-download');
    var saved = global.document.getElementById('phrase-saved');
    var proceed = global.document.getElementById('phrase-continue');

    saved.addEventListener('change', function () {
      proceed.disabled = !saved.checked;
    });

    copy.addEventListener('click', async function () {
      try {
        await UI.copyText(phraseState.phrase);
        UI.toast('Recovery phrase copied. Store it somewhere offline.');
      } catch (error) {
        UI.toast('Could not copy the phrase. Select the words and copy them manually.', 'error');
      }
    });

    download.addEventListener('click', function () {
      var payload = {
        warning: 'This file restores your Vartalap identity. Anyone who has it can read your messages.',
        username: phraseState.username,
        userId: phraseState.userId,
        createdAt: new Date().toISOString(),
        recoveryPhrase: phraseState.phrase
      };
      var blob = new global.Blob([JSON.stringify(payload, null, 2)], { type: 'application/json' });
      var url = global.URL.createObjectURL(blob);
      var link = global.document.createElement('a');
      link.href = url;
      link.download = 'vartalap-recovery-' + phraseState.username + '.json';
      global.document.body.appendChild(link);
      link.click();
      global.document.body.removeChild(link);
      global.setTimeout(function () { global.URL.revokeObjectURL(url); }, 1000);
    });

    proceed.addEventListener('click', function () {
      global.location.replace('/');
    });

    return function show(phrase, username, userId, backup) {
      phraseState = { phrase: phrase, username: username, userId: userId, backup: backup };
      grid.textContent = '';
      phrase.split(' ').forEach(function (word, index) {
        var item = global.document.createElement('li');
        var number = global.document.createElement('span');
        number.className = 'phrase-number';
        number.textContent = String(index + 1);
        var text = global.document.createElement('span');
        text.className = 'phrase-word';
        text.textContent = word;
        item.appendChild(number);
        item.appendChild(text);
        grid.appendChild(item);
      });
      saved.checked = false;
      proceed.disabled = true;
      if (typeof dialog.showModal === 'function') {
        dialog.showModal();
      } else {
        dialog.setAttribute('open', '');
      }
    };
  }

  async function uploadAvatar(file, userId) {
    if (!file) {
      return 0;
    }
    var form = new global.FormData();
    form.append('avatar', file, file.name || 'avatar');
    var result = await UI.api('/api/profile/avatar', { method: 'POST', body: form });
    return result && result.avatarUrl ? Date.now() : 0;
  }

  function bindCreate(showPhrase) {
    var form = global.document.getElementById('create-form');
    var button = global.document.getElementById('create-submit');
    var note = global.document.getElementById('create-status');

    form.addEventListener('submit', async function (event) {
      event.preventDefault();
      status(note, '', false);
      var username = form.username.value.trim().toLowerCase();
      var displayName = form.displayName.value.trim();
      var deviceName = form.deviceName.value.trim();
      if (!username || !displayName) {
        status(note, 'Choose a username and a display name.', true);
        return;
      }
      var deviceId = Crypto.deviceIdFromCookie();
      if (!deviceId) {
        status(note, 'This browser has no session yet. Reload the page and try again.', true);
        return;
      }
      UI.setBusy(button, true, 'Generating keys…');
      try {
        var identity = await Crypto.generateIdentity();
        var phrase = await Crypto.generateMnemonic();
        var backup = await Crypto.encryptBackup(identity, phrase);
        var timestamp = Date.now();
        var canonical = Crypto.canonicalRegistration(
          deviceId, username, displayName, identity.encPublicKey, identity.sigPublicKey, timestamp);
        var signature = await Crypto.sign(identity.sigPrivateKey, canonical);

        status(note, 'Creating your account…', false);
        var response = await UI.api('/api/identity/register', {
          method: 'POST',
          json: {
            username: username,
            displayName: displayName,
            deviceName: deviceName,
            encPublicKey: identity.encPublicKey,
            sigPublicKey: identity.sigPublicKey,
            timestamp: timestamp,
            signature: signature,
            backup: backup
          }
        });

        var avatarUpdatedAt = 0;
        if (form.avatar.files && form.avatar.files.length > 0) {
          status(note, 'Uploading your public avatar…', false);
          try {
            avatarUpdatedAt = await uploadAvatar(form.avatar.files[0], response.user.id);
          } catch (avatarError) {
            UI.toast('Your identity was created, but the avatar could not be uploaded.', 'error');
          }
        }

        await DB.saveIdentity({
          userId: response.user.id,
          username: response.user.username,
          displayName: response.user.displayName,
          avatarUpdatedAt: avatarUpdatedAt,
          encPrivateKey: identity.encPrivateKey,
          sigPrivateKey: identity.sigPrivateKey,
          encPublicKey: identity.encPublicKey,
          sigPublicKey: identity.sigPublicKey,
          fingerprint: identity.fingerprint,
          createdAt: Date.now()
        });

        UI.setBusy(button, false);
        showPhrase(phrase, response.user.username, response.user.id, backup);
      } catch (error) {
        UI.setBusy(button, false);
        status(note, error.message || 'Could not create the identity.', true);
      }
    });
  }

  function bindRestore() {
    var form = global.document.getElementById('restore-form');
    var button = global.document.getElementById('restore-submit');
    var note = global.document.getElementById('restore-status');

    form.addEventListener('submit', async function (event) {
      event.preventDefault();
      status(note, '', false);
      var username = form.username.value.trim().toLowerCase();
      var phrase = Crypto.normalizeMnemonic(form.phrase.value);
      var deviceName = form.deviceName.value.trim();
      if (!username) {
        status(note, 'Enter your username.', true);
        return;
      }
      if (!(await Crypto.validateMnemonic(phrase))) {
        status(note, 'That is not a valid twelve-word recovery phrase.', true);
        return;
      }
      var deviceId = Crypto.deviceIdFromCookie();
      if (!deviceId) {
        status(note, 'This browser has no session yet. Reload the page and try again.', true);
        return;
      }
      UI.setBusy(button, true, 'Checking phrase…');
      try {
        var started = await UI.api('/api/identity/recovery/start', {
          method: 'POST',
          json: { username: username }
        });
        var backup = started.backup;
        var material;
        try {
          material = await Crypto.decryptBackup(backup, phrase);
        } catch (error) {
          throw new Error('That phrase does not unlock this identity. Check the words and their order.');
        }
        var identity = await Crypto.importIdentity(material.encJwk, material.sigJwk);
        if (identity.encPublicKey !== started.encPublicKey || identity.sigPublicKey !== started.sigPublicKey) {
          throw new Error('The restored keys do not match this identity.');
        }

        var timestamp = Date.now();
        var canonical = Crypto.canonicalRecovery(started.userId, deviceId, started.challenge, timestamp);
        var signature = await Crypto.sign(identity.sigPrivateKey, canonical);

        status(note, 'Rebinding this device…', false);
        var response = await UI.api('/api/identity/recovery/complete', {
          method: 'POST',
          json: {
            challenge: started.challenge,
            timestamp: timestamp,
            signature: signature,
            deviceName: deviceName
          }
        });

        await DB.clearMessagingData();
        await DB.saveIdentity({
          userId: response.user.id,
          username: response.user.username,
          displayName: response.user.displayName,
          avatarUpdatedAt: response.user.avatarUpdatedAt || 0,
          encPrivateKey: identity.encPrivateKey,
          sigPrivateKey: identity.sigPrivateKey,
          encPublicKey: identity.encPublicKey,
          sigPublicKey: identity.sigPublicKey,
          fingerprint: identity.fingerprint,
          createdAt: Date.now()
        });
        UI.toast('Identity restored on this device.');
        global.location.replace('/');
      } catch (error) {
        UI.setBusy(button, false);
        status(note, error.message || 'Could not restore the identity.', true);
      }
    });
  }

  async function start() {
    UI.bindTheme('#theme-toggle');
    bindTabs();
    var showPhrase = bindPhraseDialog();
    try {
      Crypto.ensureCrypto();
    } catch (error) {
      var note = global.document.getElementById('create-status');
      status(note, error.message, true);
      global.document.getElementById('create-submit').disabled = true;
      global.document.getElementById('restore-submit').disabled = true;
      return;
    }
    var existing = await DB.getIdentity();
    if (existing && existing.userId) {
      global.location.replace('/');
      return;
    }
    bindCreate(showPhrase);
    bindRestore();
  }

  if (global.document.readyState === 'loading') {
    global.document.addEventListener('DOMContentLoaded', start);
  } else {
    start();
  }
})(window);
