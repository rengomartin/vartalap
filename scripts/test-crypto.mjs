/*
 * Client-cryptography smoke tests. Run with:
 *   node scripts/test-crypto.mjs
 *
 * Node 20+ exposes WebCrypto globally, so the browser module can be exercised
 * directly with a tiny window shim. These tests cover the exact wire format
 * the Go server verifies.
 */
import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');

globalThis.window = globalThis;
globalThis.isSecureContext = true;
globalThis.btoa = (value) => Buffer.from(value, 'binary').toString('base64');
globalThis.atob = (value) => Buffer.from(value, 'base64').toString('binary');
globalThis.document = { cookie: '' };

for (const file of ['static/js/bip39-wordlist.js', 'static/js/crypto.js']) {
  // eslint-disable-next-line no-eval
  eval(fs.readFileSync(path.join(root, file), 'utf8'));
}

const Crypto = window.VartalapCrypto;
assert.ok(Crypto, 'VartalapCrypto should be defined');

const alice = await Crypto.generateIdentity();
const bob = await Crypto.generateIdentity();
assert.equal(alice.encPublicKey.length, 88, 'uncompressed P-256 key is 65 bytes of base64');
assert.equal(alice.sigPublicKey.length, 88);

const phrase = await Crypto.generateMnemonic();
assert.equal(phrase.split(' ').length, 12);
assert.equal(await Crypto.validateMnemonic(phrase), true);
assert.equal(await Crypto.validateMnemonic(phrase.replace(/\w+$/, 'abandon')), false);

const backup = await Crypto.encryptBackup(alice, phrase);
const restoredMaterial = await Crypto.decryptBackup(backup, phrase);
const restored = await Crypto.importIdentity(restoredMaterial.encJwk, restoredMaterial.sigJwk);
assert.equal(restored.encPublicKey, alice.encPublicKey);
assert.equal(restored.sigPublicKey, alice.sigPublicKey);
assert.equal(restored.fingerprint, alice.fingerprint);

const conversationId = Crypto.newUUID();
const envelope = await Crypto.encryptMessage({
  conversationId,
  senderUserId: Crypto.newUUID(),
  senderDeviceId: Crypto.newUUID(),
  recipientUserId: Crypto.newUUID(),
  recipientEncPublicKey: bob.encPublicKey,
  senderSigPublicKey: alice.sigPublicKey,
  senderSigPrivateKey: alice.sigPrivateKey,
  type: 'text',
  payload: { v: 1, kind: 'text', text: 'a private hello' }
});

assert.equal(
  await Crypto.verify(envelope.senderSigningKey, envelope.signature, Crypto.canonicalMessage(envelope)),
  true,
  'sender signature verifies');

const plaintext = await Crypto.decryptMessage(envelope, bob.encPrivateKey);
assert.equal(plaintext.text, 'a private hello');

await assert.rejects(
  () => Crypto.decryptMessage(envelope, alice.encPrivateKey),
  'the wrong recipient cannot decrypt');

const tampered = { ...envelope, ciphertext: envelope.ciphertext.slice(0, -4) + 'AAAA' };
await assert.rejects(
  () => Crypto.decryptMessage(tampered, bob.encPrivateKey),
  'AES-GCM rejects tampered ciphertext');

const bytes = new Uint8Array([1, 2, 3, 4, 5, 250]);
const attachment = await Crypto.encryptAttachment(bytes);
const attachmentPlaintext = await Crypto.decryptAttachment(
  attachment.ciphertext, attachment.key, attachment.iv);
assert.deepEqual(Array.from(attachmentPlaintext), Array.from(bytes));

console.log('client crypto: all checks passed');
