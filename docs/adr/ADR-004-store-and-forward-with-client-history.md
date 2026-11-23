# ADR-004: Store-and-forward with client-authoritative history

- **Status:** Accepted
- **Date:** 2026-11-23

## Context

Recipients are often offline. Messages must not be lost, but the server must not
become a permanent plaintext archive (it cannot read ciphertext anyway, but
retaining it forever increases exposure and storage). The client also needs a
usable history when the network is unavailable.

## Decision

Use a store-and-forward mailbox with per-device delivery state, and make the
client's IndexedDB the authoritative history.

- The sender encrypts and posts an envelope.
- The server stores it in `encrypted_messages` and creates one
  `message_delivery` row per recipient device with state `queued`.
- On WebSocket connect or HTTP pending drain, the server hands over envelopes
  and marks them `delivered`.
- The client verifies, decrypts and stores a message locally, then sends an
  `ack`.
- The server marks the delivery `acked`. When every recipient device is `acked`,
  it deletes the envelope; delivery rows cascade.
- The sender receives a `delivery` event.
- A sender may delete an envelope only while all delivery rows are still
  `queued` (no recipient has received it).
- Undelivered envelopes expire after 30 days; attachments after 7 days.

Idempotency:

- The message ID is client-generated; `INSERT OR IGNORE` makes retries safe while
  the row exists.
- The client deduplicates by message ID in IndexedDB.

## Consequences

- Offline delivery works without push infrastructure.
- The server holds ciphertext only temporarily, reducing exposure.
- "Delivered" means "the recipient device verified, decrypted and stored it",
  which is a stronger signal than a socket write.
- History is lost if every device that stored a message is lost. Recovery
  restores keys, not messages.
- After an ack deletes the row, the server no longer remembers the ID, so a
  captured envelope could be replayed subject to client dedup. This is a
  documented limitation.
- If the client cannot decrypt (wrong device, corrupt state), it does not ack;
  the server retries until expiry.
- Multi-device fan-out is representable but not implemented in this release.

## Alternatives considered

- **Keep all ciphertext forever.** Rejected: unnecessary retention and exposure.
- **Delete on socket write.** Rejected: a socket write is not proof of storage;
  the client might crash before persisting.
- **Server-authoritative history.** Rejected: it would require the server to
  store ciphertext indefinitely and would make local-first operation harder.
- **Client-side only with no server queue.** Rejected: no offline delivery.
