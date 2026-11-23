# ADR-008: IndexedDB local storage and one active device

- **Status:** Accepted
- **Date:** 2026-11-23

## Context

The server deletes ciphertext after acknowledgement, so the browser must own
message history. Private keys must not be exportable during normal operation.
The first release also needs a tractable multi-device story.

## Decision

Use IndexedDB as the authoritative client store, and support one active device
per identity in this release while keeping the schema multi-device-ready.

### IndexedDB

Database `vartalap`, version 1, with stores:

| Store | Key | Purpose |
| --- | --- | --- |
| `identity` | `"self"` | user/device IDs, public keys, non-extractable private keys, fingerprint |
| `contacts` | `userId` | public keys, fingerprint, verified/identityChanged flags |
| `conversations` | `id` | peer summary, preview, unread count, timestamps |
| `messages` | `id` | plaintext history, attachment metadata, direction, state; indexes on conversation and sort time |
| `outbox` | `id` | encrypted envelope awaiting delivery |
| `settings` | `key` | small preferences |

Private keys are imported with `extractable: false`. Raw JWKs exist only at
creation, to build the recovery backup, and in memory while importing a
decrypted backup during recovery.

### One active device

- Registration creates one device.
- Recovery revokes every existing device and binds the new transport device with
  the same public keys.
- `message_delivery` is already per device, so fan-out is representable.
- `MAX_DEVICES_PER_USER` is reserved and defaults to 1; there is no add-device
  flow.

## Consequences

- The client works offline and after the server deletes ciphertext.
- Losing the browser loses history; recovery restores keys only.
- Non-extractable keys reduce accidental export but do **not** protect against
  XSS or a compromised browser profile, because same-origin script can use the
  keys through WebCrypto.
- One device keeps the first release simple: no device-linking protocol, no
  per-device key distribution, no multi-device read state.
- Adding multi-device later requires:
  - a linking/authorization flow;
  - publishing each device's public keys;
  - encrypting each message once per recipient device (or a shared session);
  - per-device acks and delivery state (already modeled);
  - UI for device management.
- IndexedDB schema changes require a `DB_VERSION` bump and an upgrade path;
  clearing the store destroys the identity.

## Alternatives considered

- **localStorage for history and keys.** Rejected: synchronous, size-limited and
  unsuitable for private key objects and message volumes.
- **Server-authoritative history.** Rejected: contradicts client-authoritative
  deletion and increases server exposure.
- **Exportable private keys in IndexedDB.** Rejected: increases the blast radius
  of a storage leak; the recovery backup already provides an export path.
- **Multi-device from day one.** Rejected: significantly larger protocol and UX
  surface; the schema keeps the door open.
