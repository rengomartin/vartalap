# Architecture

Vartalap is a single Go process that serves an embedded browser client and an
embedded SQLite database. The browser owns all plaintext and private keys; the
server is a relay and mailbox for opaque ciphertext.

## System overview

```
┌──────────────────────────── Browser A ────────────────────────────┐
│  templates + Carbon UI                                             │
│  crypto.js   WebCrypto: ECDH / ECDSA / HKDF / AES-GCM / PBKDF2     │
│  db.js       IndexedDB: identity, contacts, conversations,         │
│              messages, outbox, settings                            │
│  app.js      send/receive, outbox, acks, WS, presence, typing      │
└───────────────┬───────────────────────────────────────┬────────────┘
                │ HTTPS (JSON, CSRF header)             │ WSS
                ▼                                       ▼
┌────────────────────────────── Vartalap server ─────────────────────┐
│  chi router                                                        │
│    middleware: security headers → logging → identity → CSRF        │
│    pages + HTMX fragments                                          │
│    /api identity, discovery, conversations, messages, attachments  │
│    /ws authenticated WebSocket hub                                 │
│  services: validation, canonical transcripts, tokens, rate limits  │
│  repository: SQLite (WAL)                                          │
│  filesystem: data/attachments (ciphertext), data/avatars (public)  │
└───────────────┬───────────────────────────────────────┬────────────┘
                │                                       │
                ▼                                       ▼
         SQLite database                       attachment blobs
     users, devices, conversations,        opaque AES-GCM ciphertext
     encrypted_messages, delivery,
     attachments, backups, challenges
```

The server can route, count, expire and delete. It cannot decrypt messages or
attachments, and it never receives a private key or recovery phrase.

## Components

### HTTP layer

`internal/handlers` builds a `chi` router. Middleware order is:

1. `Recoverer` — turn panics into 500s.
2. `SecurityHeaders` — CSP, `nosniff`, `DENY` framing, no-referrer,
   permissions policy.
3. `RequestLogger` — method, URI, status, duration.
4. `Identity` — resolve or issue a signed device token.
5. `CSRF` — ensure a double-submit token exists and expose it to templates.

Pages are rendered server-side. The messenger is a single page (`/`) whose
JavaScript takes over after load. Discovery results are HTMX fragments served by
`/discover` and `/discover/search`.

### Identity and sessions

Two separate concepts:

- **Cryptographic identity** — ECDH and ECDSA key pairs generated in the
  browser. Public keys are stored in `devices`. Private keys stay in IndexedDB.
- **Transport session** — an HMAC-signed device token in `vartalap_id`
  (`HttpOnly`) and `vartalap_sync` (JS-readable), plus an optional
  `X-Vartalap-Device` header. It authenticates HTTP and WebSocket requests but
  is not the cryptographic identity.

The token format is `v1.<uuid>.<HMAC-SHA256>`, signed with a 32-byte secret in
`app_secrets`. The secret survives restarts, so deploys do not log everyone out.
If a cookie is missing or invalid, the middleware issues a fresh device ID; if
one of the two cookies survived, it repairs the other.

### Cryptographic identity

Each device has:

- an **ECDH P-256** key pair used to receive messages;
- an **ECDSA P-256** key pair used to sign registration, recovery and messages.

The public keys are exported as base64 of the 65-byte uncompressed point. The
private keys are re-imported as non-extractable `CryptoKey` objects. The JWK
form exists only long enough to build the encrypted recovery backup at creation
time, and again in memory while importing a decrypted backup during recovery.

### Conversations

A conversation is uniquely identified by the unordered pair of users.
`conversations` stores `user_a < user_b` with a unique index, and
`conversation_members` stores both participants. `GetOrCreateConversation`
converges concurrent creation on the unique index. There is no group chat.

### Message envelope

The server sees exactly this shape:

```json
{
  "id": "uuid",
  "conversationId": "uuid",
  "senderUserId": "uuid",
  "senderDeviceId": "uuid",
  "recipientUserId": "uuid",
  "type": "text" | "attachment",
  "ephemeralPublicKey": "base64 P-256 point",
  "iv": "base64 12 bytes",
  "ciphertext": "base64 AES-GCM output",
  "signature": "base64 64-byte r||s",
  "senderSigningKey": "base64 P-256 point",
  "clientCreatedAt": 1730000000000
}
```

The plaintext payload inside `ciphertext` is client-defined JSON:

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

The server validates structure, identity binding and signature; it cannot read
any of the payload fields.

### Delivery lifecycle

```
Sender                         Server                          Recipient
  │                              │                                │
  │ 1. fetch key bundle          │                                │
  │─────────────────────────────▶│                                │
  │ 2. encrypt + sign            │                                │
  │ 3. POST envelope             │                                │
  │─────────────────────────────▶│ validate, INSERT OR IGNORE     │
  │                              │ queue message_delivery         │
  │                              │────────── WS message ─────────▶│ 4. verify sig
  │                              │                                │ 5. decrypt
  │                              │                                │ 6. store IndexedDB
  │                              │◀────────── WS ack ─────────────│ 7. ack
  │                              │ mark acked; delete if all acked│
  │◀────────── WS delivery ──────│                                │
```

Important semantics:

- `queued` → `delivered` when the envelope is handed to the recipient (HTTP
  pending drain or WS connect/send). `delivered` here means "reached the
  device", not "read".
- `acked` means the client verified, decrypted and stored it. The server sends a
  `delivery` event to the sender and deletes the envelope once every recipient
  device is `acked`.
- The recipient's local history is authoritative. The server is a temporary
  mailbox, not an archive.
- The sender receives an echo of its own message over WS so other tabs can
  reconcile; clients deduplicate by message ID.

### Offline store-and-forward

When the recipient is offline, the envelope and a `message_delivery` row remain
queued. On the next WebSocket connection the server enqueues up to 200 pending
messages before starting the writer; the client may also call
`GET /api/messages/pending`. The client acknowledges only after successful
decryption and storage. If decryption fails (for example, the wrong device), it
does not ack, so the envelope remains until retention expiry and may be retried.

The send queue is 512 events, sized to hold a full pending backlog plus live
traffic. A client that cannot keep up is disconnected rather than blocking the
hub.

### Attachments

1. The browser generates a random 32-byte AES-256 key and a 12-byte IV.
2. It encrypts the file with AES-256-GCM and AAD `vartalap:attachment:v1`.
3. It uploads the ciphertext as `application/octet-stream`.
4. The server stores an opaque blob and metadata (conversation, uploader, size,
   timestamps). No filename, MIME type or plaintext size is stored.
5. The key and IV are placed inside the end-to-end encrypted message payload.
6. The recipient downloads the ciphertext, checks conversation membership, and
   decrypts locally.

Attachments expire after `ATTACHMENT_RETENTION_DAYS` (default 7). An upload that
is never referenced by a message becomes an orphan until expiry.

### Recovery

1. At registration the browser generates a 12-word BIP-39 phrase, derives a
   PBKDF2-SHA-256 key (210,000 iterations), and encrypts the identity JWKs into
   a backup blob stored on the server.
2. On a new browser, `POST /api/identity/recovery/start` with the username
   returns the backup and a single-use challenge valid for five minutes.
3. The browser decrypts the backup locally, imports the keys, checks they match
   the public keys in the response, and signs the canonical recovery transcript
   with the old signing key.
4. `POST /api/identity/recovery/complete` consumes the challenge, verifies the
   signature against the old device's signing key, revokes all existing devices,
   and binds the new transport device to the identity.

The phrase never leaves the browser. Recovery does not restore message history:
history exists only on devices that received and stored it. It also does not
recover an identity whose backup was never created.

### Presence and typing

Presence is derived from open WebSocket connections, not a database field. The
first connection for a user notifies conversation partners; the last
disconnect notifies them offline. New connections receive the current presence
of their partners in the `ready` event.

Typing is ephemeral. The client sends `typing` start/stop for the active
conversation; the server verifies membership, throttles to one event per 1.5
seconds per conversation per connection, and forwards it to the peer. Nothing is
persisted.

### Retention janitor

An hourly goroutine:

- deletes `encrypted_messages` past `expires_at` (default 30 days);
- deletes expired attachment metadata and their blob files (default 7 days);
- deletes expired recovery challenges.

Deletion is opaque: the server removes data it cannot read.

### Rendering and assets

Templates and static assets are embedded with `go:embed`. Static responses are
served from an in-memory map with gzip and a one-hour cache. There is no runtime
CDN or UI build step. HTMX is used only for discovery fragments; the messenger
uses plain `fetch` and `WebSocket`.

## Trust boundaries

| Boundary | Trusted for | Not trusted for |
| --- | --- | --- |
| Browser origin | Running the client code, holding keys | Resisting XSS, malicious extensions, compromised device |
| Server | Routing, ordering, retention, session auth | Reading plaintext, proving who a human is |
| Other users | Nothing beyond their public keys | Honesty about key changes (hence fingerprints) |
| Network | TLS in production | Confidentiality without TLS; use HTTPS/WSS |

## Scaling and deployment shape

This release is designed for a single process:

- the WebSocket hub is in-memory;
- the rate limiter is in-memory;
- SQLite is opened with one connection and WAL;
- attachment files live on local disk.

Horizontal scaling would require sticky WebSocket routing or a shared pub/sub
layer, a shared limiter, and either a networked database or per-instance shards.
That is deliberately out of scope. A reverse proxy must forward `Origin`,
`Upgrade` and `Connection` for `/ws`, and `X-Forwarded-Proto` so cookies are
marked `Secure` correctly.

## Why these choices

- **Simple primitives over a ratchet.** The project prioritizes a scheme that a
  reviewer can understand end to end. The cost is no forward secrecy or
  post-compromise security; see [ADR-002](adr/ADR-002-webcrypto-sealed-envelope.md)
  and [SECURITY.md](SECURITY.md).
- **Server as a mailbox.** Client-authoritative history keeps the server from
  becoming a plaintext archive and makes deletion meaningful.
- **One conversation per pair.** It removes room membership, invitations and
  group-key management from the first release.
- **Embedded everything.** A single binary plus a data directory is easy to
  deploy and audit, and avoids supply-chain surprises from a CDN.

## Related documents

- [SECURITY.md](SECURITY.md) — threat model and exact cryptographic construction
- [API.md](API.md) — endpoints and WebSocket events
- [DATABASE.md](DATABASE.md) — tables, indexes and retention
- [CODEBASE_MAP.md](CODEBASE_MAP.md) — file-level map
