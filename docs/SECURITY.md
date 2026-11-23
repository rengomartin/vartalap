# Security model

This document describes what Vartalap protects, how, and what it does **not**
protect. It is intentionally explicit about limitations. If the code and this
document disagree, treat it as a security bug.

## 1. Summary

Vartalap provides:

- **Message confidentiality** against the server and network observers, using
  ECDH P-256 + HKDF-SHA-256 + AES-256-GCM.
- **Sender authentication** for each envelope, using ECDSA P-256 + SHA-256 over
  a canonical transcript.
- **Attachment confidentiality**, using a random AES-256-GCM key delivered
  inside the encrypted message.
- **Identity recovery** using a BIP-39 phrase that unlocks a client-encrypted
  backup, plus a signature over a single-use server challenge.
- **Key-change detection** via a stored per-contact fingerprint.

Vartalap does **not** provide:

- forward secrecy;
- post-compromise security;
- a double ratchet or any key evolution;
- protection against a compromised device, malicious browser extension or XSS;
- anonymity against the server or network (usernames, pairs, timing and sizes
  are visible);
- cryptographic deniability (signatures are non-repudiable);
- replay protection after a message is acknowledged and deleted;
- multi-device message sync (one active device per identity in this release);
- a key transparency log (key discovery trusts the server plus out-of-band
  fingerprint comparison).

## 2. Threat model

### In scope

| Adversary | Goal | Mitigation |
| --- | --- | --- |
| Honest-but-curious server operator | Read message or attachment content | Client-side E2EE; server only stores ciphertext |
| Network observer | Read or modify traffic | TLS/WSS in production; signatures detect envelope tampering |
| Another user | Impersonate a sender, read a conversation | Session binding, envelope signature verification, membership checks |
| Another user | Harvest discovery data | Directory visibility toggle; exact-username reachability remains |
| Malicious client replaying a captured envelope | Duplicate or resurrect a message | Server ID idempotency while stored; client dedup by message ID |
| Contact key substitution by the server | MITM a conversation | Stored fingerprints and out-of-band verification (limited on first contact) |

### Out of scope

- A compromised operating system, browser, or browser extension.
- An attacker who can execute JavaScript in the Vartalap origin (XSS defeats
  E2EE because the keys are usable by same-origin script).
- A user who shares their recovery phrase or leaves it where others can read it.
- Denial of service beyond basic rate limiting.
- Traffic analysis and metadata correlation.
- Coercion or legal compulsion of the server operator or a device owner.

## 3. Primitives and parameters

All cryptography uses the browser's WebCrypto API. No third-party crypto library
is bundled.

| Purpose | Primitive | Parameters |
| --- | --- | --- |
| Identity encryption key | ECDH P-256 | static per device |
| Identity signing key | ECDSA P-256 + SHA-256 | static per device |
| Per-message key agreement | ECDH P-256 | fresh ephemeral key per message |
| Key derivation | HKDF-SHA-256 | salt `vartalap:message-salt:v1`, info binds conversation and message ID |
| Message encryption | AES-256-GCM | 12-byte random IV, 128-bit tag, AAD binds metadata |
| Envelope signature | ECDSA P-256 + SHA-256 | raw `r||s`, 64 bytes |
| Fingerprint | SHA-256 | first 16 bytes, hex, grouped in fours |
| Recovery phrase | BIP-39 | 128-bit entropy, 12 words, 4-bit checksum |
| Backup KDF | PBKDF2-SHA-256 | 210,000 iterations, 16-byte random salt |
| Backup encryption | AES-256-GCM | 12-byte random IV, 128-bit tag |
| Attachment encryption | AES-256-GCM | random 32-byte key, 12-byte random IV, AAD `vartalap:attachment:v1` |

Wire encodings:

- Public key: standard padded base64 of the 65-byte uncompressed P-256 point
  (`0x04 || x || y`).
- Signature: standard padded base64 of 64 bytes (`r || s`, 32 bytes each).
- IV: standard padded base64 of 12 bytes.
- Ciphertext: standard padded base64.
- Message IDs, conversation IDs, user IDs and device IDs: canonical UUID v4.

The Go server decodes and validates keys, signatures, IVs and envelope shape. It
does not derive keys or decrypt anything.

## 4. Identity creation

On registration the browser:

1. generates an ECDH P-256 key pair and an ECDSA P-256 key pair;
2. exports the public keys as raw uncompressed points;
3. re-imports the private keys as **non-extractable** `CryptoKey` objects;
4. generates a 12-word BIP-39 phrase;
5. derives a backup key with PBKDF2-SHA-256 (210,000 iterations);
6. encrypts the private JWKs with AES-256-GCM;
7. signs the registration transcript with the signing key.

Registration transcript (newline-joined, UTF-8):

```
vartalap:register:v1
<deviceId>
<username>
<displayName>
<encPublicKey>
<sigPublicKey>
<timestampMillis>
```

The server:

- derives the device ID from the signed session, never from the request body;
- validates username, display name and both public keys;
- rejects timestamps more than 10 minutes from server time;
- verifies the signature against `sigPublicKey`;
- requires a valid encrypted backup;
- creates the user, device and backup in one transaction.

This proves the registrant controls the signing key. It does not prove a unique
human, and a user may create multiple identities.

## 5. Message encryption

For each message the sender:

1. generates a fresh ephemeral ECDH P-256 key pair;
2. performs ECDH with the recipient device's static public key;
3. derives a 256-bit AES-GCM key with HKDF-SHA-256:

   ```
   salt = "vartalap:message-salt:v1"
   info = "vartalap:message:v1|<conversationId>|<messageId>"
   ```

4. encrypts the JSON payload with a random 12-byte IV and 128-bit tag;
5. signs the canonical envelope.

Message transcript (newline-joined, UTF-8):

```
vartalap:message:v1
<id>
<conversationId>
<senderUserId>
<senderDeviceId>
<recipientUserId>
<type>
<ephemeralPublicKey>
<iv>
<ciphertext>
```

AES-GCM additional authenticated data (newline-joined):

```
vartalap:message:v1
<id>
<conversationId>
<senderUserId>
<senderDeviceId>
<recipientUserId>
<type>
<ephemeralPublicKey>
```

The AAD binds the ciphertext to its routing metadata. The signature additionally
binds the IV and ciphertext. The recipient verifies the signature **before**
decrypting.

The server verifies:

- the envelope is structurally valid and within size limits;
- `senderUserId` and `senderDeviceId` match the authenticated session;
- `recipientUserId` is the other member of the conversation;
- `senderSigningKey` equals the device's registered signing key;
- the signature verifies over the canonical transcript.

Because the signature covers routing fields, the server cannot silently
re-address an envelope without invalidating it.

### Key change detection

The client computes a fingerprint from both public keys:

```
SHA-256("vartalap:fingerprint:v1|" + encPublicKey + "|" + sigPublicKey)
```

The first 16 bytes are shown as eight groups of four hex characters. The client
stores the fingerprint per contact. If a later key bundle has a different
fingerprint, the client marks the contact as changed, clears its verified flag,
and warns the user. The user can compare the new fingerprint out of band and
mark it verified.

Limitations:

- On **first contact**, there is no prior fingerprint. The client trusts the
  bundle served by the server. Out-of-band comparison is the only defence
  against a malicious server substituting keys at that moment.
- There is no key transparency log or gossip protocol.
- A server that consistently serves attacker keys can MITM a user who never
  verifies fingerprints.

## 6. Delivery, acknowledgement and deletion

The server stores an envelope and one `message_delivery` row per recipient
device:

- `queued` — stored, not yet handed to the device.
- `delivered` — handed to the device over HTTP pending drain or WebSocket.
- `acked` — the client verified, decrypted and stored it.

When every recipient device is `acked`, the server deletes the envelope (and
delivery rows cascade). The sender receives a `delivery` event.

Consequences:

- "Delivered" in the UI means the recipient device acknowledged decryption and
  local storage, not that a human read it.
- The server cannot re-send an acknowledged message; local history is
  authoritative.
- A sender may delete an envelope only while **no delivery has left `queued`**.
  After that, the sender's delete action removes only the local copy.
- A recipient's "Remove" action removes only the local copy.
- Retention deletes undelivered envelopes after `MESSAGE_RETENTION_DAYS`
  (default 30) and attachments after `ATTACHMENT_RETENTION_DAYS` (default 7).

### Replay caveat

While an envelope row exists, a duplicate send with the same ID is ignored
(`INSERT OR IGNORE`). After all devices ack and the row is deleted, the server no
longer remembers the ID. A malicious party who captured a valid envelope and
has a live session cookie could replay it; the client deduplicates by message ID
only if it still has the message locally. There is no server-side replay cache
and no anti-rollback mechanism. Treat this as a known limitation, not a
guarantee.

## 7. Recovery

### Backup

At registration the browser encrypts a JSON document containing the private JWKs
and public keys with a key derived from the 12-word phrase:

```
PBKDF2-SHA-256(phrase, salt, 210000) -> AES-256-GCM key
```

The server stores `ciphertext`, `iv`, `kdfSalt` and `kdfIterations`. It never
sees the phrase or the plaintext JWKs.

### Rebind flow

1. `POST /api/identity/recovery/start {username}` returns a random 32-byte
   challenge (hex), the user's public keys and the encrypted backup. This
   endpoint is unauthenticated but rate-limited, because a new browser has no
   identity yet.
2. The browser derives the backup key from the phrase, decrypts the JWKs,
   imports the keys, and checks that the derived public keys match the ones in
   the response.
3. The browser signs the recovery transcript with the **old** signing key:

   ```
   vartalap:recover:v1
   <userId>
   <newDeviceId>
   <challenge>
   <timestampMillis>
   ```

4. `POST /api/identity/recovery/complete` consumes the challenge, verifies the
   signature against the old device's registered signing key, revokes every
   existing device, and binds the new transport device to the identity with the
   same public keys.

Properties and limits:

- Possession of the phrase alone is not enough to rebind; the attacker must also
  decrypt the backup and hold the old signing key. The phrase **is** the
  decryption key, so in practice phrase possession is identity possession.
- The challenge is single-use and expires after five minutes.
- The challenge is consumed **before** signature verification. A caller who
  obtains a challenge can burn it with an invalid signature, causing a retry
  (rate-limited). This is a deliberate simplicity trade-off, not a
  denial-of-service guarantee.
- Recovery does not restore message history. History exists only on devices that
  received and stored it.
- Recovery revokes the old device. The old browser's cookie no longer resolves
  to an active device and its API calls return `401`.
- `recovery/start` reveals the encrypted backup to anyone who knows the
  username. The backup is only as strong as the phrase and PBKDF2. The phrase is
  generated with 128 bits of entropy; do not replace it with a hand-picked
  low-entropy phrase that happens to pass the BIP-39 checksum.
- There is no rate limit on offline guessing against a downloaded backup. Use
  the generated phrase.

## 8. Attachments

The browser encrypts file bytes with a random AES-256-GCM key and uploads only
ciphertext. The key and IV travel inside the encrypted message payload. The
server stores:

- an opaque blob at `data/attachments/<uuid>.bin`;
- conversation ID, uploader ID, ciphertext size and timestamps.

It does not store a filename, MIME type, plaintext size or hash. Downloads
require conversation membership. Blobs expire after seven days by default.

Limitations:

- If the message never reaches the recipient, or the blob expires before
  download, the file is unavailable. The client does not keep attachment
  plaintext in IndexedDB, only metadata, so it cannot re-upload automatically.
- An upload that is never referenced by a message is an orphan until expiry.
- Ciphertext size is visible and approximates plaintext size plus a small
  overhead.

## 9. Sessions, CSRF and transport

### Device session

The transport session is an HMAC-signed token:

```
v1.<deviceUUID>.<base64url(HMAC-SHA256(secret, "vartalap-device-token:v1:" + deviceUUID))>
```

- `vartalap_id` is `HttpOnly`, `SameSite=Lax`, 400 days, `Secure` on HTTPS.
- `vartalap_sync` is JS-readable so the client can repair a lost cookie;
  `SameSite=Lax`, 400 days, `Secure` on HTTPS.
- `X-Vartalap-Device` is accepted for header-based restore flows.
- The signing secret is generated on first run and stored in `app_secrets`, so
  tokens survive restarts.

What a stolen session token allows:

- read public profile and directory data;
- list conversations and fetch pending **ciphertext**;
- download attachment **ciphertext**;
- change the display name, username, visibility and avatar;
- revoke the device or start recovery (but not complete it without the phrase);
- delete undelivered messages;
- replay a previously captured valid envelope.

What it does **not** allow without the private keys:

- decrypt messages or attachments;
- forge a new signed envelope;
- complete recovery.

The session token is therefore an authorization credential for metadata and
destructive actions, not the cryptographic identity. Protect it like a password.

### CSRF

State-changing requests require the double-submit token:

- cookie `vartalap_csrf` (JS-readable, `SameSite=Lax`);
- header `X-CSRF-Token` (or form field `_csrf` for form posts);
- constant-time comparison;
- the header is checked before parsing form bodies so raw attachment uploads are
  not consumed.

### WebSocket

- Requires a valid device session.
- `Origin` must equal the request host (honouring `X-Forwarded-Host`) or match
  `ALLOWED_ORIGINS`. Origin must have no userinfo, query or fragment, and a path
  of empty or `/`.
- Maximum five connections per user.
- 64 KiB inbound frame limit.
- Ping/pong keepalive; a slow client is closed instead of blocking the hub.

### Response headers

```
Content-Security-Policy: default-src 'self'; script-src 'self'; style-src 'self';
  img-src 'self' data: blob:; media-src 'self' blob:; font-src 'self';
  connect-src 'self' ws: wss:; object-src 'none'; base-uri 'self';
  form-action 'self'; frame-ancestors 'none'
X-Content-Type-Options: nosniff
X-Frame-Options: DENY
Referrer-Policy: no-referrer
Permissions-Policy: camera=(), microphone=(), geolocation=()
```

There are no inline scripts. Client modules are ES5-style IIFEs loaded from the
same origin.

### Rate limiting

In-memory token buckets, keyed by device or user ID:

| Bucket | Refill | Burst | Used for |
| --- | --- | --- | --- |
| `register` | 1 / 600 s | 5 | identity creation |
| `discovery` | 1 / s | 60 | directory, profile, key bundle |
| `search` | 0.5 / s | 20 | username search |
| `messages` | 1 / s | 30 | send and pending drain |
| `attachments` | 1 / 180 s | 10 | upload/download |
| `recovery` | 0.2 / s | 5 | recovery start/complete |
| `profile` | 0.5 / s | 20 | profile and avatar changes |
| `ack` | 2 / s | 60 | acknowledgements |
| `ws-connect` | 0.2 / s | 10 | WebSocket upgrades |
| `general` | `RATE_PER_SECOND` / s | `RATE_BURST` | conversation creation, WS ack/typing |

Limits are per process and reset on restart. They slow abuse; they are not a
security boundary.

## 10. What the server can see

The server necessarily sees:

- usernames, display names, avatars and directory visibility;
- which two users share a conversation;
- message IDs, sender/recipient IDs, type (`text` or `attachment`), timestamps,
  ciphertext sizes, IVs, ephemeral public keys and signatures;
- attachment ciphertext sizes and upload times;
- delivery and acknowledgement state;
- IP addresses and request metadata at the HTTP layer;
- request URIs in logs, including usernames in paths and search queries.

It cannot see plaintext, private keys, recovery phrases or attachment keys.

## 11. Known limitations (read this)

1. **No forward secrecy.** The recipient's static ECDH private key decrypts
   every captured message addressed to it. Compromise of that key compromises
   all past ciphertext the attacker has.
2. **No post-compromise security.** There is no ratchet to heal after a key
   compromise.
3. **No deniability.** ECDSA signatures bind the sender device to each
   ciphertext. A recipient who can decrypt can also demonstrate the sender's
   signature over it.
4. **Trust on first use for keys.** The server supplies key bundles. Verify
   fingerprints out of band, especially before sensitive conversations.
5. **XSS is fatal.** Non-extractable keys can still be *used* by same-origin
   script. CSP and same-origin scripts reduce but do not eliminate this risk.
6. **Device compromise is fatal.** Malware, a malicious extension, or a
   compromised browser profile can read decrypted history or act as the device.
7. **Metadata is not protected.** Timing, pairings, sizes and activity are
   visible.
8. **Replay after deletion is possible.** See §6.
9. **One active device.** Recovery revokes the old device; messages are not
   fanned out to multiple devices.
10. **Attachment availability is temporary.** Blobs expire; local clients keep
    only metadata.
11. **Recovery is all-or-nothing.** Lose the phrase, lose the identity. The
    server cannot reset it.
12. **No account deletion endpoint.** "Forget this device" revokes the device
    and clears local data; the username and public profile remain. There is no
    server-side erasure workflow for a user row.
13. **No audit log for the user.** The server does not expose a login/device
    history to the account owner.
14. **Single-process assumptions.** In-memory hub and limiter; SQLite single
    writer. A multi-instance deployment would change the security and
    availability properties.

## 12. Operational guidance

- Always serve over HTTPS/WSS. WebCrypto requires a secure context, and cookies
  are marked `Secure` only on HTTPS.
- Set `ALLOWED_ORIGINS` if a proxy rewrites the host; forward `Origin`,
  `Upgrade`, `Connection` and `X-Forwarded-Proto`.
- Keep `data/vartalap.db` and `data/attachments/` backed up together. The
  database holds the token-signing secret and all delivery state.
- Protect the data directory from other local users. Attachment blobs and the
  database are ciphertext/metadata, but the signing secret and session state are
  sensitive.
- Monitor for repeated 401/403/429 responses and unexpected `/api/identity/`
  traffic.
- Rotate the token-signing secret only with a planned logout of all sessions
  (delete the `identity_signing_key` row and restart).
- Do not add third-party scripts to the origin; the E2EE guarantee depends on
  the integrity of the served JavaScript.

## 13. Cryptographic versioning

Domain strings carry a `:v1` suffix:

- `vartalap:register:v1`
- `vartalap:recover:v1`
- `vartalap:message:v1`
- `vartalap:fingerprint:v1`
- `vartalap:attachment:v1`
- `vartalap:message-salt:v1`

Changing a transcript, KDF parameter or envelope field requires a version bump
and a migration/compatibility plan. Never silently change one side of a
client/server pair. Add tests in both `internal/services/crypto_test.go` and
`scripts/test-crypto.mjs`.

## 14. Reporting

If you find a vulnerability, do not open a public issue with exploit details.
Contact the maintainer privately, include a minimal reproduction, and allow time
for a fix and coordinated disclosure.
