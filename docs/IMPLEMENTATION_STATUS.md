# Implementation status

This page is an honest snapshot of what is built, what is partial, and what is
deliberately deferred. Update it when behavior changes.

Last reviewed against the current source tree.

## Summary

| Area | Status |
| --- | --- |
| Private 1:1 conversations | **Complete** |
| Anonymous identity and unique usernames | **Complete** |
| Browser E2EE (sealed envelope) | **Complete** |
| Sender signatures and server verification | **Complete** |
| Offline store-and-forward + acks | **Complete** |
| Local IndexedDB history | **Complete** |
| Encrypted attachments | **Complete** |
| Replies | **Complete** |
| Typing and presence | **Complete** |
| Delivery status | **Complete** (delivered/acked only; no read receipts) |
| Key-change detection and manual verification | **Complete** |
| Recovery phrase and device rebinding | **Complete** |
| Public profiles and shareable links | **Complete** |
| Directory and username search | **Complete** |
| Profile editing UI after onboarding | **Partial** (API exists; UI is read-only) |
| Multi-device | **Deferred** (schema-ready, one active device) |
| Forward secrecy / ratchet | **Deferred** (documented limitation) |
| Signal / PQXDH | **Deferred** (see ADR-002) |
| Group chat | **Out of scope** |
| Account deletion / data export | **Not implemented** |
| Notifications, blocking, reporting | **Not implemented** |

## Complete

### Identity

- Anonymous registration with unique username, display name, optional device
  name and optional public avatar.
- Browser-generated ECDH P-256 encryption key and ECDSA P-256 signing key.
- Private keys stored as non-extractable `CryptoKey` objects in IndexedDB.
- Server stores only public keys, profile data and ciphertext.
- Signed registration transcript with a 10-minute timestamp window.
- HMAC-signed device session cookies with repair from a sync mirror and a
  header fallback.
- Reserved usernames, username normalization, display-name and device-name
  validation.
- "Forget this device" revokes devices and clears local data.

### Messaging

- One conversation per unordered user pair, enforced by a unique index.
- Envelope encryption: ephemeral ECDH P-256 + HKDF-SHA-256 + AES-256-GCM.
- Per-message ECDSA signature over a canonical transcript; verified by the
  server and by the recipient.
- AES-GCM AAD binds routing metadata.
- Store-and-forward queue with `queued` → `delivered` → `acked` states.
- Pending drain over HTTP and automatic flush on WebSocket connect.
- Sender-side delete only while no delivery has left `queued`; recipient
  "Remove" is local only.
- Server deletes an envelope once every recipient device acks.
- Client deduplicates by message ID and keeps an outbox for retries.
- Replies with quoted preview inside the encrypted payload.
- Attachment encryption with a random AES-256 key delivered inside the message.
- Delivery notifications to the sender.
- Message retention (30 days) and attachment retention (7 days) with an hourly
  janitor.

### Real time

- Authenticated WebSocket with same-origin/allow-list checks.
- Presence derived from connections, fanned out to conversation partners.
- Typing indicators with client and server throttling.
- Ping/pong keepalive, bounded send queue, slow-client eviction.
- Up to five sockets per user; clients deduplicate echoes by message ID.

### Discovery and profiles

- Public profile page at `/u/{username}` with shareable link and copy action.
- Directory of discoverable users with cursor pagination (24 per page).
- Partial username search (20 results, exact matches first).
- Visibility toggle via the API.
- Public avatar upload (PNG/JPEG/WebP) and cache-busted serving.

### Recovery

- BIP-39 12-word phrase (128-bit entropy) generated at registration.
- PBKDF2-SHA-256 (210,000 iterations) + AES-256-GCM backup stored on the server.
- Recovery start returns the backup and a single-use, five-minute challenge.
- Recovery complete verifies a signature from the old signing key, revokes all
  devices and binds the new transport device.
- Challenge consumption is atomic and single-use.
- Recovery phrase shown once with copy/download and a confirmation gate.

### Security and operations

- Strict CSP, framing denial, no-referrer, nosniff, permissions policy.
- Double-submit CSRF with constant-time comparison.
- Per-bucket in-memory rate limiting.
- Parameterized SQL, embedded migrations, WAL, foreign keys.
- Generic client errors, detailed server logs, no secrets in logs.
- Graceful shutdown and retention janitor.
- Versioned embedded migrations; legacy public tables preserved and unused.

### Tests and docs

- Go unit, repository, middleware, render and end-to-end HTTP/WebSocket tests.
- Node client-crypto smoke test covering the exact wire formats.
- This documentation set and ADR-001..008.

## Partial

### Profile editing after onboarding

`POST /api/profile` and `POST /api/profile/avatar` are implemented and tested.
The messenger settings dialog currently displays the username, device,
fingerprint and a link to the public profile, but does **not** expose forms to
change the display name, username, visibility or avatar after onboarding.
Onboarding does support an avatar upload. Adding the UI is a straightforward
follow-up.

### Read receipts

The protocol exposes `queued`, `delivered` and `acked`. The UI labels the
sender's state as "sent" and "delivered". There is no separate "read" event and
no per-message read timestamp.

### Multi-device

The schema separates users from devices and `message_delivery` is per device, so
fan-out is representable. The current release creates one active device per
identity, and recovery revokes the old one. There is no UI or API to add a
second device, and `MAX_DEVICES_PER_USER` is not enforced beyond the
single-active-device flow.

### Message history UX

The client loads all locally stored messages for a conversation at once. There is
no pagination, search or export UI. This is fine for the expected volumes but
will need work for very long histories.

### Attachment handling

The client keeps attachment metadata but not plaintext bytes. If the server blob
expires or was never uploaded, the user cannot re-download it from the app.
Orphan uploads expire with the janitor. There is no progress indicator beyond
the composer hint text.

## Deferred / not implemented

- **Forward secrecy and post-compromise security.** No ratchet. See ADR-002 and
  SECURITY.md.
- **Signal Protocol / PQXDH.** Evaluated and intentionally deferred; the official
  client is Node-only and browser implementations carry GPL-3.0 obligations that
  conflict with this MIT project. See ADR-002.
- **Group conversations.** Out of scope for the private 1:1 model.
- **Account deletion and data export.** "Forget this device" is not account
  deletion; the user row and public profile remain.
- **Blocking, muting, reporting and abuse controls.**
- **Key transparency / gossip.** Key discovery trusts the server plus manual
  fingerprint verification.
- **Push notifications and background delivery.**
- **Server-side message search.** The server cannot read messages.
- **Voice/video calls.**
- **Multi-instance deployment.** The hub and limiter are in-memory and SQLite is
  single-writer.
- **Localized UI.** English only.

## Known security limitations

These are properties of the design, not bugs:

- No forward secrecy; a static recipient key decrypts captured ciphertext.
- No post-compromise security.
- Non-repudiable signatures (no deniability).
- Trust-on-first-use key discovery.
- XSS or device compromise defeats E2EE.
- Metadata (pairs, timing, sizes, activity) is visible to the server.
- Replay is possible after a message is acked and deleted, subject to client
  dedup.
- Recovery reveals the encrypted backup to anyone who knows the username; its
  strength depends on the generated phrase.
- A stolen session cookie allows metadata access, destructive actions and
  ciphertext download, but not decryption or new message forgery.
- Avatars are public.
- No audit log for the account owner.

See [`SECURITY.md`](SECURITY.md) for details.

## Suggested next steps

1. Add profile editing (display name, username, visibility, avatar) to the
   settings dialog.
2. Add a conversation/message search over IndexedDB and an export action.
3. Add attachment download retry and a clear "expired" state.
4. Add read receipts if the product needs them.
5. Design multi-device linking and per-device fan-out, then enforce
   `MAX_DEVICES_PER_USER`.
6. Evaluate a vetted, permissively licensed ratchet for forward secrecy; if
   adopted, bump the message domain and support a transition.
7. Add account deletion semantics and a retention policy for user rows.
8. Add structured security logging and alerting for repeated auth failures.
