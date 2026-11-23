/*
 * Vartalap client cryptography.
 *
 * Design: a deliberately simple "sealed envelope" scheme built only from
 * vetted WebCrypto primitives. It is NOT the Signal Protocol and provides no
 * forward secrecy or post-compromise security. See docs/SECURITY.md.
 *
 * Per message:
 *   1. ephemeral ECDH P-256 key pair
 *   2. ECDH with the recipient's static P-256 public key
 *   3. HKDF-SHA-256 -> AES-256-GCM
 *   4. ECDSA P-256 SHA-256 signature over a canonical transcript
 */
(function (global) {
  'use strict';

  var DOMAIN_REGISTER = 'vartalap:register:v1';
  var DOMAIN_RECOVER = 'vartalap:recover:v1';
  var DOMAIN_MESSAGE = 'vartalap:message:v1';
  var DOMAIN_FINGERPRINT = 'vartalap:fingerprint:v1';
  var ATTACHMENT_AAD = 'vartalap:attachment:v1';
  var MESSAGE_SALT = 'vartalap:message-salt:v1';
  var BACKUP_ITERATIONS = 210000;

  var encoder = new TextEncoder();
  var decoder = new TextDecoder();

  function ensureCrypto() {
    if (!global.isSecureContext) {
      throw new Error('Vartalap needs a secure context (HTTPS or localhost) to encrypt messages.');
    }
    if (!global.crypto || !global.crypto.subtle || !global.crypto.getRandomValues) {
      throw new Error('This browser does not support Web Crypto, so messages cannot be encrypted.');
    }
  }

  function randomBytes(length) {
    var bytes = new Uint8Array(length);
    global.crypto.getRandomValues(bytes);
    return bytes;
  }

  function toBase64(bytes) {
    var view = bytes instanceof Uint8Array ? bytes : new Uint8Array(bytes);
    var binary = '';
    for (var i = 0; i < view.length; i += 1) {
      binary += String.fromCharCode(view[i]);
    }
    return global.btoa(binary);
  }

  function fromBase64(value) {
    var binary = global.atob(value);
    var bytes = new Uint8Array(binary.length);
    for (var i = 0; i < binary.length; i += 1) {
      bytes[i] = binary.charCodeAt(i);
    }
    return bytes;
  }

  function utf8(value) {
    return encoder.encode(value);
  }

  async function sha256(bytes) {
    return new Uint8Array(await global.crypto.subtle.digest('SHA-256', bytes));
  }

  async function fingerprint(encPublicKey, sigPublicKey) {
    var digest = await sha256(utf8(DOMAIN_FINGERPRINT + '|' + encPublicKey + '|' + sigPublicKey));
    var hex = '';
    for (var i = 0; i < 16; i += 1) {
      hex += digest[i].toString(16).padStart(2, '0');
    }
    var groups = [];
    for (var j = 0; j < hex.length; j += 4) {
      groups.push(hex.slice(j, j + 4));
    }
    return groups.join(' ');
  }

  async function generateIdentity() {
    ensureCrypto();
    var encPair = await global.crypto.subtle.generateKey(
      { name: 'ECDH', namedCurve: 'P-256' }, true, ['deriveBits']);
    var sigPair = await global.crypto.subtle.generateKey(
      { name: 'ECDSA', namedCurve: 'P-256' }, true, ['sign', 'verify']);

    var encPublicKey = toBase64(await global.crypto.subtle.exportKey('raw', encPair.publicKey));
    var sigPublicKey = toBase64(await global.crypto.subtle.exportKey('raw', sigPair.publicKey));
    var encJwk = await global.crypto.subtle.exportKey('jwk', encPair.privateKey);
    var sigJwk = await global.crypto.subtle.exportKey('jwk', sigPair.privateKey);

    // Re-import private keys as non-extractable so normal operation cannot
    // export them again. The one-time JWKs are only used for the recovery blob.
    var encPrivateKey = await global.crypto.subtle.importKey(
      'jwk', encJwk, { name: 'ECDH', namedCurve: 'P-256' }, false, ['deriveBits']);
    var sigPrivateKey = await global.crypto.subtle.importKey(
      'jwk', sigJwk, { name: 'ECDSA', namedCurve: 'P-256' }, false, ['sign']);

    return {
      encPrivateKey: encPrivateKey,
      sigPrivateKey: sigPrivateKey,
      encPublicKey: encPublicKey,
      sigPublicKey: sigPublicKey,
      encJwk: encJwk,
      sigJwk: sigJwk,
      fingerprint: await fingerprint(encPublicKey, sigPublicKey)
    };
  }

  async function importRawPublic(algorithm, base64Key) {
    return global.crypto.subtle.importKey(
      'raw', fromBase64(base64Key), { name: algorithm, namedCurve: 'P-256' }, true,
      algorithm === 'ECDH' ? [] : ['verify']);
  }

  async function sign(privateKey, text) {
    var signature = await global.crypto.subtle.sign(
      { name: 'ECDSA', hash: 'SHA-256' }, privateKey, utf8(text));
    return toBase64(signature);
  }

  async function verify(publicKeyBase64, signatureBase64, text) {
    try {
      var key = await importRawPublic('ECDSA', publicKeyBase64);
      return await global.crypto.subtle.verify(
        { name: 'ECDSA', hash: 'SHA-256' }, key, fromBase64(signatureBase64), utf8(text));
    } catch (error) {
      return false;
    }
  }

  function canonicalRegistration(deviceId, username, displayName, encPublicKey, sigPublicKey, timestamp) {
    return [DOMAIN_REGISTER, deviceId, username, displayName, encPublicKey, sigPublicKey, String(timestamp)].join('\n');
  }

  function canonicalRecovery(userId, deviceId, nonce, timestamp) {
    return [DOMAIN_RECOVER, userId, deviceId, nonce, String(timestamp)].join('\n');
  }

  function canonicalMessage(envelope) {
    return [
      DOMAIN_MESSAGE,
      envelope.id,
      envelope.conversationId,
      envelope.senderUserId,
      envelope.senderDeviceId,
      envelope.recipientUserId,
      envelope.type,
      envelope.ephemeralPublicKey,
      envelope.iv,
      envelope.ciphertext
    ].join('\n');
  }

  function messageAAD(envelope) {
    return [
      DOMAIN_MESSAGE,
      envelope.id,
      envelope.conversationId,
      envelope.senderUserId,
      envelope.senderDeviceId,
      envelope.recipientUserId,
      envelope.type,
      envelope.ephemeralPublicKey
    ].join('\n');
  }

  async function deriveMessageKey(sharedSecret, conversationId, messageId) {
    var hkdfKey = await global.crypto.subtle.importKey('raw', sharedSecret, 'HKDF', false, ['deriveKey']);
    return global.crypto.subtle.deriveKey(
      {
        name: 'HKDF',
        hash: 'SHA-256',
        salt: utf8(MESSAGE_SALT),
        info: utf8(DOMAIN_MESSAGE + '|' + conversationId + '|' + messageId)
      },
      hkdfKey,
      { name: 'AES-GCM', length: 256 },
      false,
      ['encrypt', 'decrypt']);
  }

  function newUUID() {
    if (global.crypto.randomUUID) {
      return global.crypto.randomUUID();
    }
    var bytes = randomBytes(16);
    bytes[6] = (bytes[6] & 0x0f) | 0x40;
    bytes[8] = (bytes[8] & 0x3f) | 0x80;
    var hex = Array.prototype.map.call(bytes, function (b) {
      return b.toString(16).padStart(2, '0');
    }).join('');
    return hex.slice(0, 8) + '-' + hex.slice(8, 12) + '-' + hex.slice(12, 16) + '-' +
      hex.slice(16, 20) + '-' + hex.slice(20);
  }

  /*
   * Encrypt a JSON payload for one recipient device. The caller supplies all
   * identity fields so the signed transcript and the server's checks match.
   */
  async function encryptMessage(options) {
    ensureCrypto();
    var id = options.id || newUUID();
    var ephemeral = await global.crypto.subtle.generateKey(
      { name: 'ECDH', namedCurve: 'P-256' }, true, ['deriveBits']);
    var ephemeralPublicKey = toBase64(
      await global.crypto.subtle.exportKey('raw', ephemeral.publicKey));
    var recipientKey = await importRawPublic('ECDH', options.recipientEncPublicKey);
    var shared = await global.crypto.subtle.deriveBits(
      { name: 'ECDH', public: recipientKey }, ephemeral.privateKey, 256);
    var key = await deriveMessageKey(shared, options.conversationId, id);

    var envelope = {
      id: id,
      conversationId: options.conversationId,
      senderUserId: options.senderUserId,
      senderDeviceId: options.senderDeviceId,
      recipientUserId: options.recipientUserId,
      type: options.type,
      ephemeralPublicKey: ephemeralPublicKey,
      iv: '',
      ciphertext: '',
      signature: '',
      senderSigningKey: options.senderSigPublicKey,
      clientCreatedAt: Date.now()
    };

    var iv = randomBytes(12);
    var ciphertext = await global.crypto.subtle.encrypt(
      { name: 'AES-GCM', iv: iv, additionalData: utf8(messageAAD(envelope)), tagLength: 128 },
      key, utf8(JSON.stringify(options.payload)));
    envelope.iv = toBase64(iv);
    envelope.ciphertext = toBase64(ciphertext);
    envelope.signature = await sign(options.senderSigPrivateKey, canonicalMessage(envelope));
    return envelope;
  }

  async function decryptMessage(envelope, encPrivateKey) {
    var ephemeralKey = await importRawPublic('ECDH', envelope.ephemeralPublicKey);
    var shared = await global.crypto.subtle.deriveBits(
      { name: 'ECDH', public: ephemeralKey }, encPrivateKey, 256);
    var key = await deriveMessageKey(shared, envelope.conversationId, envelope.id);
    var plaintext = await global.crypto.subtle.decrypt(
      {
        name: 'AES-GCM',
        iv: fromBase64(envelope.iv),
        additionalData: utf8(messageAAD(envelope)),
        tagLength: 128
      },
      key,
      fromBase64(envelope.ciphertext));
    return JSON.parse(decoder.decode(plaintext));
  }

  async function encryptAttachment(bytes) {
    ensureCrypto();
    var rawKey = randomBytes(32);
    var iv = randomBytes(12);
    var key = await global.crypto.subtle.importKey('raw', rawKey, 'AES-GCM', false, ['encrypt']);
    var ciphertext = await global.crypto.subtle.encrypt(
      { name: 'AES-GCM', iv: iv, additionalData: utf8(ATTACHMENT_AAD), tagLength: 128 },
      key, bytes);
    return {
      ciphertext: new Uint8Array(ciphertext),
      key: toBase64(rawKey),
      iv: toBase64(iv)
    };
  }

  async function decryptAttachment(ciphertext, keyBase64, ivBase64) {
    var key = await global.crypto.subtle.importKey(
      'raw', fromBase64(keyBase64), 'AES-GCM', false, ['decrypt']);
    var plaintext = await global.crypto.subtle.decrypt(
      { name: 'AES-GCM', iv: fromBase64(ivBase64), additionalData: utf8(ATTACHMENT_AAD), tagLength: 128 },
      key, ciphertext);
    return new Uint8Array(plaintext);
  }

  /* ---- BIP-39 recovery phrase (128-bit / 12 words) ---------------------- */

  function wordlist() {
    if (!global.VARTALAP_BIP39 || global.VARTALAP_BIP39.length !== 2048) {
      throw new Error('The recovery wordlist failed to load.');
    }
    return global.VARTALAP_BIP39;
  }

  async function generateMnemonic() {
    ensureCrypto();
    var entropy = randomBytes(16);
    var digest = await sha256(entropy);
    var bits = '';
    for (var i = 0; i < entropy.length; i += 1) {
      bits += entropy[i].toString(2).padStart(8, '0');
    }
    bits += digest[0].toString(2).padStart(8, '0').slice(0, 4);
    var words = wordlist();
    var phrase = [];
    for (var j = 0; j < 12; j += 1) {
      phrase.push(words[parseInt(bits.slice(j * 11, j * 11 + 11), 2)]);
    }
    return phrase.join(' ');
  }

  function normalizeMnemonic(phrase) {
    return String(phrase || '').trim().toLowerCase().split(/\s+/).filter(Boolean).join(' ');
  }

  async function validateMnemonic(phrase) {
    var normalized = normalizeMnemonic(phrase);
    var parts = normalized ? normalized.split(' ') : [];
    if (parts.length !== 12) {
      return false;
    }
    var words = wordlist();
    var bits = '';
    for (var i = 0; i < parts.length; i += 1) {
      var index = words.indexOf(parts[i]);
      if (index < 0) {
        return false;
      }
      bits += index.toString(2).padStart(11, '0');
    }
    var entropy = new Uint8Array(16);
    for (var j = 0; j < 16; j += 1) {
      entropy[j] = parseInt(bits.slice(j * 8, j * 8 + 8), 2);
    }
    var digest = await sha256(entropy);
    var checksum = digest[0].toString(2).padStart(8, '0').slice(0, 4);
    return bits.slice(128) === checksum;
  }

  async function deriveBackupKey(mnemonic, salt, iterations) {
    var base = await global.crypto.subtle.importKey(
      'raw', utf8(normalizeMnemonic(mnemonic).normalize('NFKD')), 'PBKDF2', false, ['deriveKey']);
    return global.crypto.subtle.deriveKey(
      { name: 'PBKDF2', salt: salt, iterations: iterations, hash: 'SHA-256' },
      base,
      { name: 'AES-GCM', length: 256 },
      false,
      ['encrypt', 'decrypt']);
  }

  async function encryptBackup(identity, mnemonic) {
    ensureCrypto();
    var salt = randomBytes(16);
    var iv = randomBytes(12);
    var key = await deriveBackupKey(mnemonic, salt, BACKUP_ITERATIONS);
    var plaintext = utf8(JSON.stringify({
      v: 1,
      encJwk: identity.encJwk,
      sigJwk: identity.sigJwk,
      encPublicKey: identity.encPublicKey,
      sigPublicKey: identity.sigPublicKey,
      createdAt: new Date().toISOString()
    }));
    var ciphertext = await global.crypto.subtle.encrypt(
      { name: 'AES-GCM', iv: iv, tagLength: 128 }, key, plaintext);
    return {
      ciphertext: toBase64(ciphertext),
      iv: toBase64(iv),
      kdfSalt: toBase64(salt),
      kdfIterations: BACKUP_ITERATIONS
    };
  }

  async function decryptBackup(backup, mnemonic) {
    ensureCrypto();
    var key = await deriveBackupKey(mnemonic, fromBase64(backup.kdfSalt), backup.kdfIterations);
    var plaintext = await global.crypto.subtle.decrypt(
      { name: 'AES-GCM', iv: fromBase64(backup.iv), tagLength: 128 },
      key,
      fromBase64(backup.ciphertext));
    return JSON.parse(decoder.decode(plaintext));
  }

  async function importIdentity(encJwk, sigJwk) {
    var encPrivateKey = await global.crypto.subtle.importKey(
      'jwk', encJwk, { name: 'ECDH', namedCurve: 'P-256' }, false, ['deriveBits']);
    var sigPrivateKey = await global.crypto.subtle.importKey(
      'jwk', sigJwk, { name: 'ECDSA', namedCurve: 'P-256' }, false, ['sign']);
    var encPublicKey = await publicKeyFromJwk(encJwk);
    var sigPublicKey = await publicKeyFromJwk(sigJwk);
    return {
      encPrivateKey: encPrivateKey,
      sigPrivateKey: sigPrivateKey,
      encPublicKey: encPublicKey,
      sigPublicKey: sigPublicKey,
      fingerprint: await fingerprint(encPublicKey, sigPublicKey)
    };
  }

  // Convert a private JWK's public coordinates into an importable public key.
  function encJwkToPublic(jwk) {
    return { kty: jwk.kty, crv: jwk.crv, x: jwk.x, y: jwk.y, ext: true };
  }

  async function publicKeyFromJwk(jwk) {
    var key = await global.crypto.subtle.importKey(
      'jwk', encJwkToPublic(jwk), { name: 'ECDH', namedCurve: 'P-256' }, true, []);
    return toBase64(await global.crypto.subtle.exportKey('raw', key));
  }

  function deviceIdFromCookie() {
    var match = global.document.cookie.match(/(?:^|;\s*)vartalap_sync=([^;]+)/);
    if (!match) {
      return '';
    }
    var token = decodeURIComponent(match[1]);
    var parts = token.split('.');
    if (parts.length !== 3) {
      return '';
    }
    return /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/.test(parts[1])
      ? parts[1] : '';
  }

  function csrfToken() {
    var meta = global.document.querySelector('meta[name="csrf-token"]');
    return meta ? meta.getAttribute('content') || '' : '';
  }

  function pageValue(name) {
    var meta = global.document.querySelector('meta[name="' + name + '"]');
    return meta ? meta.getAttribute('content') || '' : '';
  }

  global.VartalapCrypto = {
    ensureCrypto: ensureCrypto,
    randomBytes: randomBytes,
    toBase64: toBase64,
    fromBase64: fromBase64,
    utf8: utf8,
    sha256: sha256,
    fingerprint: fingerprint,
    generateIdentity: generateIdentity,
    importIdentity: importIdentity,
    importRawPublic: importRawPublic,
    publicKeyFromJwk: publicKeyFromJwk,
    sign: sign,
    verify: verify,
    canonicalRegistration: canonicalRegistration,
    canonicalRecovery: canonicalRecovery,
    canonicalMessage: canonicalMessage,
    encryptMessage: encryptMessage,
    decryptMessage: decryptMessage,
    encryptAttachment: encryptAttachment,
    decryptAttachment: decryptAttachment,
    generateMnemonic: generateMnemonic,
    normalizeMnemonic: normalizeMnemonic,
    validateMnemonic: validateMnemonic,
    encryptBackup: encryptBackup,
    decryptBackup: decryptBackup,
    newUUID: newUUID,
    deviceIdFromCookie: deviceIdFromCookie,
    csrfToken: csrfToken,
    pageValue: pageValue,
    BACKUP_ITERATIONS: BACKUP_ITERATIONS
  };
})(window);
