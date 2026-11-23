# API reference

Vartalap exposes an HTML surface, a JSON API and an authenticated WebSocket. All
endpoints are same-origin.

## Conventions

### Sessions and CSRF

- A browser session is established automatically by middleware: a signed device
  token in `vartalap_id` (HttpOnly) and `vartalap_sync` (JS-readable), or an
  `X-Vartalap-Device` header.
- Everything under `/api` except `POST /api/identity/register`,
  `POST /api/identity/recovery/start` and
  `POST /api/identity/recovery/complete` requires an active device session and
  returns `401` without one.
- State-changing requests (`POST`, `DELETE`) require the double-submit CSRF
  token: send the `vartalap_csrf` cookie value in `X-CSRF-Token`. Missing or
  mismatched tokens return `403 csrf_failed`.
- JSON responses use `Cache-Control: no-store`.

### JSON rules

- Request bodies must be `application/json` and are decoded with unknown fields
  rejected and trailing data rejected.
- Error body:

  ```json
  { "error": "machine_code", "message": "Human-readable explanation." }
  ```

- Successful bodies are documented per endpoint. Unknown fields in a response
  should be ignored by clients.

### Body limits

| Endpoint | Limit |
| --- | --- |
| Registration | 64 KiB |
| Recovery start | 4 KiB |
| Recovery complete | 8 KiB |
| Profile update | 8 KiB |
| Create conversation | 4 KiB |
| Send message | 160 KiB |
| Ack | 16 KiB |
| Attachment upload | `MAX_ATTACHMENT_BYTES` (10 MiB default) |
| Avatar upload | `MAX_AVATAR_BYTES` (512 KiB default) |
| WebSocket frame | 64 KiB |

### Status codes

| Code | Meaning |
| --- | --- |
| 200 | Success |
| 201 | Created (registration, attachment upload) |
| 400 | Validation error |
| 401 | No active device session |
| 403 | CSRF, membership, sender mismatch or signature failure |
| 404 | Unknown user, conversation, attachment or page |
| 409 | Conflict (username taken, already registered, already delivered, no device) |
| 413 | Upload too large |
| 415 | Wrong attachment content type |
| 429 | Rate limited |
| 500 | Internal error |

## HTML routes

| Method | Path | Description |
| --- | --- | --- |
| `GET` | `/` | Onboarding page without an identity; messenger otherwise |
| `GET` | `/u/{username}` | Public profile page (404 page if unknown) |
| `GET` | `/avatar/{userID}` | Public avatar image, cache-busted by `?v=` |
| `GET` | `/healthz` | `{"status":"ok"}` |
| `GET` | `/static/*` | Embedded CSS, JS, fonts and SVG assets |
| `GET` | `/discover?cursor=` | HTMX fragment: directory page (auth) |
| `GET` | `/discover/search?q=` | HTMX fragment: search results (auth) |
| `GET` | `/ws` | WebSocket upgrade (auth) |

`/discover` and `/discover/search` return HTML, not JSON. The fragment target is
`#discover-results`.

## Identity

### `POST /api/identity/register`

Creates a user, its first device and the encrypted identity backup. Requires the
device cookie and CSRF. The device must not already be registered.

Request:

```json
{
  "username": "alice_22",
  "displayName": "Alice",
  "deviceName": "Home laptop",
  "encPublicKey": "base64 65-byte P-256 point",
  "sigPublicKey": "base64 65-byte P-256 point",
  "timestamp": 1730000000000,
  "signature": "base64 64-byte ECDSA r||s",
  "backup": {
    "ciphertext": "base64 AES-256-GCM",
    "iv": "base64 12 bytes",
    "kdfSalt": "base64 16 bytes",
    "kdfIterations": 210000
  }
}
```

`signature` is over:

```
vartalap:register:v1
<deviceId>
<username>
<displayName>
<encPublicKey>
<sigPublicKey>
<timestamp>
```

`deviceName` is optional (defaults to `This device`). `backup` is required.

Response `201`:

```json
{
  "user": {
    "id": "uuid",
    "username": "alice_22",
    "displayName": "Alice",
    "avatarUpdatedAt": 0,
    "discoverable": true
  },
  "device": {
    "id": "uuid",
    "name": "Home laptop",
    "createdAt": 1730000000000,
    "lastSeenAt": 1730000000000
  },
  "fingerprint": "a1b2 c3d4 e5f6 0718 293a 4b5c 6d7e 8f90"
}
```

Errors: `invalid_device`, `invalid_username`, `invalid_display_name`,
`invalid_device_name`, `invalid_key`, `stale_proof`, `invalid_proof`,
`invalid_backup`, `already_registered`, `username_taken`, `rate_limited`,
`csrf_failed`.

### `POST /api/identity/recovery/start`

Returns the encrypted backup and a single-use challenge. Unauthenticated but
rate-limited.

Request:

```json
{ "username": "alice_22" }
```

Response `200`:

```json
{
  "challenge": "64 hex characters",
  "userId": "uuid",
  "username": "alice_22",
  "displayName": "Alice",
  "encPublicKey": "base64",
  "sigPublicKey": "base64",
  "backup": {
    "ciphertext": "base64",
    "iv": "base64",
    "kdfSalt": "base64",
    "kdfIterations": 210000
  }
}
```

Errors: `invalid_username`, `not_found`, `no_device`, `no_backup`,
`rate_limited`, `csrf_failed`.

### `POST /api/identity/recovery/complete`

Consumes the challenge, verifies a signature from the old device's signing key,
revokes all existing devices and binds the current transport device.

Request:

```json
{
  "challenge": "64 hex characters",
  "timestamp": 1730000000000,
  "signature": "base64 64-byte ECDSA r||s",
  "deviceName": "New laptop"
}
```

`signature` is over:

```
vartalap:recover:v1
<userId>
<newDeviceId>
<challenge>
<timestamp>
```

Response `200` is the same shape as registration.

Errors: `stale_proof`, `invalid_challenge`, `invalid_device_name`,
`invalid_device`, `invalid_proof`, `not_found`, `no_device`, `rate_limited`,
`csrf_failed`.

> The challenge is consumed before the signature is checked. A failed attempt
> burns it; request a new one.

## Current identity and profile

### `GET /api/me`

Response `200`:

```json
{
  "user": { "id": "uuid", "username": "alice_22", "displayName": "Alice",
            "avatarUpdatedAt": 0, "discoverable": true },
  "device": { "id": "uuid", "name": "Home laptop",
              "createdAt": 1730000000000, "lastSeenAt": 1730000000000 },
  "fingerprint": "a1b2 c3d4 e5f6 0718 293a 4b5c 6d7e 8f90",
  "hasRecoveryBackup": true
}
```

### `POST /api/profile`

Updates the display name, optional username and optional directory visibility.
Requires CSRF.

Request:

```json
{
  "displayName": "Alice L.",
  "username": "alice_l",
  "discoverable": false
}
```

`username` and `discoverable` are optional; omitted values are left unchanged
(`username` is only changed if it differs after normalization).

Response `200`:

```json
{ "user": { "id": "uuid", "username": "alice_l", "displayName": "Alice L.",
            "avatarUpdatedAt": 0, "discoverable": false } }
```

Errors: `invalid_display_name`, `invalid_username`, `username_taken`,
`rate_limited`, `csrf_failed`.

### `POST /api/profile/avatar`

`multipart/form-data` with a field named `avatar`. Accepts PNG, JPEG or WebP up
to `MAX_AVATAR_BYTES`. Requires CSRF. Avatars are **public**.

Response `200`:

```json
{ "avatarUrl": "/avatar/uuid?v=20261123120000" }
```

Errors: `invalid_upload`, `too_large`, `invalid_type`, `rate_limited`,
`csrf_failed`.

### `POST /api/forget`

Revokes every device for the user and clears the identity cookies. The client
should clear IndexedDB. The user row and public profile remain.

Request: `{}` (CSRF required).

Response `200`:

```json
{ "ok": true }
```

## Discovery

### `GET /api/directory?cursor=<username>`

Returns up to 24 discoverable users ordered by username, excluding the caller.
`cursor` is the last username from the previous page.

Response `200`:

```json
{
  "users": [
    { "id": "uuid", "username": "bob", "displayName": "Bob",
      "avatarUpdatedAt": 0, "discoverable": true }
  ],
  "nextCursor": "bob"
}
```

`nextCursor` is present when a full page of 24 users was returned; the following
request may then return no users.

### `GET /api/users/search?q=<query>`

Partial username search over discoverable users, excluding the caller. Query is
trimmed and capped at 32 characters; results are capped at 20. Exact matches sort
first.

Response `200`:

```json
{ "users": [ { "id": "uuid", "username": "bob", "displayName": "Bob",
               "avatarUpdatedAt": 0, "discoverable": true } ] }
```

### `GET /api/users/{username}`

Public summary of any existing user, including hidden ones (hidden only affects
directory/search).

Response `200`:

```json
{ "user": { "id": "uuid", "username": "bob", "displayName": "Bob",
            "avatarUpdatedAt": 0, "discoverable": false } }
```

### `GET /api/keys/{username}`

Public key bundle needed to encrypt to the user's active device.

Response `200`:

```json
{
  "bundle": {
    "userId": "uuid",
    "username": "bob",
    "displayName": "Bob",
    "avatarUpdatedAt": 0,
    "deviceId": "uuid",
    "encPublicKey": "base64 65-byte P-256 point",
    "sigPublicKey": "base64 65-byte P-256 point"
  },
  "fingerprint": "...."
}
```

Errors: `not_found`, `no_device`, `rate_limited`.

## Conversations

### `POST /api/conversations`

Creates or returns the single conversation with a user. Requires CSRF.

Request:

```json
{ "username": "bob" }
```

Response `200`:

```json
{
  "conversation": {
    "id": "uuid",
    "peer": { "id": "uuid", "username": "bob", "displayName": "Bob",
              "avatarUpdatedAt": 0, "discoverable": true },
    "createdAt": 1730000000000
  },
  "bundle": { "...": "same KeyBundle shape as /api/keys" }
}
```

Errors: `invalid_username`, `not_found`, `self`, `no_device`, `rate_limited`,
`csrf_failed`.

### `GET /api/conversations`

Response `200`:

```json
{
  "conversations": [
    { "id": "uuid",
      "peer": { "id": "uuid", "username": "bob", "displayName": "Bob",
                "avatarUpdatedAt": 0, "discoverable": true },
      "createdAt": 1730000000000 }
  ]
}
```

### `GET /api/conversations/{id}`

Returns the conversation and the peer's current key bundle. Requires membership.

Response `200`: same shape as create.

Errors: `not_found` (also for non-members), `no_device`.

## Messages

### Envelope shape

```json
{
  "id": "uuid",
  "conversationId": "uuid",
  "senderUserId": "uuid",
  "senderDeviceId": "uuid",
  "recipientUserId": "uuid",
  "type": "text",
  "ephemeralPublicKey": "base64 65-byte P-256 point",
  "iv": "base64 12 bytes",
  "ciphertext": "base64 AES-256-GCM",
  "signature": "base64 64-byte ECDSA r||s",
  "senderSigningKey": "base64 65-byte P-256 point",
  "clientCreatedAt": 1730000000000
}
```

`type` is `text` or `attachment`. The server sets `serverCreatedAt` itself.
`conversationId` in the body is overwritten by the path value.

The plaintext payload is opaque to the server; the client currently sends:

```json
{
  "v": 1,
  "kind": "text",
  "text": "hello",
  "sentAt": 1730000000000,
  "replyTo": { "id": "uuid", "author": "Name", "preview": "…" },
  "attachment": {
    "id": "uuid",
    "key": "base64 32-byte AES key",
    "iv": "base64 12 bytes",
    "name": "report.pdf",
    "mime": "application/pdf",
    "size": 12345
  }
}
```

### `POST /api/conversations/{id}/messages`

Stores and routes an envelope. Requires CSRF and membership. The server checks
the sender user/device against the session, the recipient against the peer, the
signing key against the device, and the signature over the canonical transcript.

Response `200`:

```json
{ "messageId": "uuid", "serverCreatedAt": 1730000000000, "duplicate": false }
```

`duplicate` is `true` when the ID already existed and no new row was inserted.

Errors: `invalid_envelope`, `forbidden`, `invalid_signature`, `no_device`,
`rate_limited`, `csrf_failed`.

### `GET /api/messages/pending?limit=200`

Returns envelopes queued for the caller's device, ordered by server time.
Marks them `delivered`. Default limit 200, maximum 500.

Response `200`:

```json
{
  "messages": [
    {
      "envelope": { "...": "Envelope shape with serverCreatedAt" },
      "sender": { "id": "uuid", "username": "alice", "displayName": "Alice",
                  "avatarUpdatedAt": 0, "discoverable": true }
    }
  ]
}
```

### `POST /api/messages/ack`

Acknowledges a batch of messages. Requires CSRF. Batch size 1–100; IDs must be
UUIDs.

Request:

```json
{ "messageIds": ["uuid", "uuid"] }
```

Response `200`:

```json
{ "ok": true }
```

Each acknowledged message whose recipient devices are all `acked` is deleted and
its sender receives a WebSocket `delivery` event.

Errors: `invalid_ack`, `rate_limited`, `csrf_failed`.

### `DELETE /api/messages/{id}`

Sender-side delete of an envelope that **no recipient device has received yet**
(all delivery rows still `queued`). Requires CSRF.

Response `200`:

```json
{ "deleted": true }
```

Response `409` when it was already delivered:

```json
{
  "deleted": false,
  "reason": "already_delivered",
  "message": "This message was already delivered. You can remove it from this device, but not from the recipient's device."
}
```

Errors: `invalid_id`, `csrf_failed`.

## Attachments

### `POST /api/conversations/{id}/attachments`

Uploads opaque ciphertext. Requires CSRF and membership. `Content-Type` must be
empty or `application/octet-stream`. The server stores no filename or MIME type.

Response `201`:

```json
{ "id": "uuid", "size": 123456, "expiresAt": 1730000000000 }
```

Errors: `forbidden`, `invalid_type`, `too_large`, `invalid_upload`,
`rate_limited`, `csrf_failed`.

### `GET /api/attachments/{id}`

Downloads the ciphertext. Requires membership. Response is
`application/octet-stream` with `Cache-Control: private, no-store` and
`Content-Disposition: attachment; filename="encrypted.bin"`.

Errors: `404` if unknown/expired, `403` if not a member.

## WebSocket

Connect to `ws(s)://<host>/ws`. The device session must be valid. `Origin` must
match the request host or `ALLOWED_ORIGINS`; otherwise the upgrade is rejected
with `403`.

Limits: five connections per user, 64 KiB inbound frames, ping every 50 seconds.

### Server → client events

| Type | Payload | Meaning |
| --- | --- | --- |
| `ready` | `{"type":"ready","userId":"uuid","onlineUserIds":["uuid"]}` | Connection established; online conversation partners |
| `message` | `{"type":"message","envelope":{…},"sender":{…}}` | New or pending envelope |
| `delivery` | `{"type":"delivery","messageId":"uuid","state":"delivered"}` | A recipient acknowledged one of your messages |
| `presence` | `{"type":"presence","userId":"uuid","online":true}` | Partner presence change |
| `typing` | `{"type":"typing","conversationId":"uuid","userId":"uuid","state":"start"}` | Partner typing state |
| `pong` | `{"type":"pong"}` | Reply to ping |
| `error` | `{"type":"error","message":"…"}` | Protocol or storage error |

On connect the server also enqueues up to 200 pending envelopes as `message`
events before the writer starts.

### Client → server events

| Type | Payload | Notes |
| --- | --- | --- |
| `ping` | `{"type":"ping"}` | Keepalive; server replies `pong` |
| `ack` | `{"type":"ack","messageIds":["uuid"]}` | 1–100 IDs; rate-limited |
| `typing` | `{"type":"typing","conversationId":"uuid","state":"start"}` | `start` or `stop`; membership checked; throttled |

The client sends `ack` after verifying, decrypting and storing a message. If the
socket is closed it falls back to `POST /api/messages/ack`.

## Error codes

| Code | Typical status | Meaning |
| --- | --- | --- |
| `invalid_request` | 400 | Malformed JSON or unknown fields |
| `invalid_device` | 400 | Missing/invalid transport device |
| `invalid_username` | 400 | Username rules violated |
| `invalid_display_name` | 400 | Display name rules violated |
| `invalid_device_name` | 400 | Device name rules violated |
| `invalid_key` | 400 | Public key not a valid P-256 point |
| `invalid_backup` | 400 | Backup fields or KDF parameters invalid |
| `invalid_envelope` | 400 | Envelope structure/limits invalid |
| `invalid_signature` | 403 | Envelope signature failed |
| `invalid_proof` | 400/403 | Registration/recovery signature failed |
| `stale_proof` | 400 | Timestamp outside the 10-minute window |
| `invalid_challenge` | 400 | Recovery challenge unknown, used or expired |
| `invalid_ack` | 400 | Empty, oversized or malformed ack batch |
| `invalid_id` | 400 | Non-UUID message ID |
| `invalid_upload` | 400 | Upload could not be read or stored |
| `invalid_type` | 400/415 | Unsupported image or attachment content type |
| `too_large` | 413 | Upload exceeds the configured limit |
| `already_registered` | 409 | Device already has an identity |
| `username_taken` | 409 | Username already exists |
| `self` | 400 | Cannot start a conversation with yourself |
| `no_device` | 409/404 | User has no active device |
| `no_backup` | 404 | Identity has no recovery backup |
| `not_found` | 404 | Resource does not exist or is not visible to you |
| `forbidden` | 403 | Not a member, sender mismatch or recipient mismatch |
| `csrf_failed` | 403 | Missing/mismatched CSRF token |
| `rate_limited` | 429 | Token bucket exhausted |
| `internal_error` | 500 | Unexpected server error |

## Rate-limit buckets

See [SECURITY.md §9](SECURITY.md#9-sessions-csrf-and-transport). Limits are
in-memory, per process, and reset on restart.

## Examples

Register and send a message with `curl` (the browser normally does the crypto):

```bash
# The device cookie is issued by any page load.
curl -c jar.txt -b jar.txt http://localhost:8080/ > /dev/null
CSRF=$(awk '$6 == "vartalap_csrf" { print $7 }' jar.txt)

curl -b jar.txt -H "X-CSRF-Token: $CSRF" -H 'Content-Type: application/json' \
  -d @register.json http://localhost:8080/api/identity/register

curl -b jar.txt http://localhost:8080/api/conversations
```

The values in `register.json` must be produced by the browser's WebCrypto code;
the server will reject invalid keys or signatures.
