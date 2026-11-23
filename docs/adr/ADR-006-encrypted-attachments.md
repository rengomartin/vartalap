# ADR-006: Encrypted attachments as opaque blobs

- **Status:** Accepted
- **Date:** 2026-11-23

## Context

Users need to send files. The server must not learn filenames, MIME types or
content, and large binaries should not travel inside the message envelope.

## Decision

Encrypt attachments separately and store them as opaque blobs.

1. The browser generates a random 32-byte AES-256-GCM key and a 12-byte IV.
2. It encrypts the file bytes with AAD `vartalap:attachment:v1`.
3. It uploads the ciphertext as `application/octet-stream` to
   `POST /api/conversations/{id}/attachments`.
4. The server stores the blob at `data/attachments/<uuid>.bin` and records only
   conversation ID, uploader ID, ciphertext size and timestamps.
5. The browser sends the key, IV, original filename, MIME type and size inside
   the end-to-end encrypted message payload.
6. The recipient downloads the ciphertext (membership checked) and decrypts it
   locally.

Attachments expire after seven days by default; the janitor removes both the
metadata row and the blob.

## Consequences

- The server cannot read files or learn their names/types.
- Large files do not inflate the message envelope.
- The attachment key is protected by the message encryption and is never stored
  server-side.
- Attachments are not available after expiry, and the client does not keep
  plaintext bytes, so it cannot re-upload automatically.
- An upload that is never referenced by a message is an orphan until expiry.
- Ciphertext size leaks approximate plaintext size.
- Downloads require an active session and conversation membership; a link alone
  is not enough.
- There is no resumable upload, virus scanning or content moderation. The server
  cannot scan ciphertext.

## Alternatives considered

- **Embed base64 file data in the message envelope.** Rejected: envelope limits
  (64 KiB ciphertext) and poor performance.
- **Store plaintext and encrypt at rest on the server.** Rejected: the server
  would see content.
- **Store the attachment key on the server.** Rejected: defeats E2EE.
- **External object storage with signed URLs.** Deferred: adds a dependency and
  URL/key leakage surface. Local disk is sufficient for a single instance.
