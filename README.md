# Vartalap

**Vartalap is an anonymous, private, end-to-end encrypted one-to-one messenger.**
There is no public room, no email address, no phone number and no password. You
pick a unique username, your browser generates your keys, and the server only
ever stores public keys and opaque ciphertext.

Built with **Go**, **SQLite**, **WebSockets**, server-rendered **Go templates**,
**HTMX** and the **IBM Carbon Design System**. No React, Vue or other frontend
framework, no UI build step, and no runtime CDN dependency: Carbon CSS, IBM Plex
fonts, logos, templates, migrations and JavaScript are embedded in the Go binary.

> **Security honesty:** Vartalap uses a deliberately simple WebCrypto "sealed
> envelope" scheme, not the Signal Protocol. It provides message
> confidentiality and sender authentication, but **no forward secrecy, no
> post-compromise security and no ratchet**. Read
> [docs/SECURITY.md](docs/SECURITY.md) before relying on it for sensitive work.

## What it does

- **Private 1:1 conversations only.** Exactly one conversation exists per pair
  of users; there is no global room and old public messages are never imported.
- **Anonymous identity.** A unique username plus a display name and optional
  public avatar. No account recovery via email or phone.
- **End-to-end encryption in the browser.** ECDH P-256 + HKDF-SHA-256 +
  AES-256-GCM, with an ECDSA P-256 signature over a canonical transcript.
- **Server never sees plaintext or private keys.** It routes ciphertext
  envelopes and stores ciphertext attachments. Local IndexedDB history is
  authoritative.
- **Offline store-and-forward.** Undelivered envelopes are queued per device,
  delivered on reconnect, and deleted from the server after the recipient
  acknowledges them.
- **Encrypted attachments.** A random AES-256-GCM key encrypts the bytes in the
  browser; the key travels only inside the end-to-end encrypted message.
- **Replies, typing indicators, presence and delivery status.**
- **Key-change detection.** The client stores a fingerprint per contact and
  warns when a contact's keys change.
- **Recovery phrase.** A twelve-word BIP-39 phrase unlocks a client-encrypted
  identity backup so you can rebind a new browser.
- **Public profile links.** Share `https://your-host/u/username`.
- **Discovery.** A directory of discoverable users plus username search. Exact
  usernames remain reachable even when a user is hidden from the directory.

## Quick start

Requirements:

- Go 1.24+
- A C compiler (the SQLite driver uses cgo)
- Node 20+ only if you want to run the client-crypto smoke test

```bash
go run ./cmd/server
```

Open <http://localhost:8080>. **WebCrypto requires a secure context:** use
`localhost` for development or HTTPS in production. Plain HTTP on a non-local
host will show a blocking error because the browser will not expose
`crypto.subtle`.

Build a binary:

```bash
make build
./bin/vartalap
```

Explicit settings:

```bash
ADDR=:3000 DB_PATH=./data/vartalap.db DATA_DIR=./data \
APP_ENV=development ALLOWED_ORIGINS=http://localhost:3000 \
go run ./cmd/server
```

## Configuration

All settings are environment variables with safe defaults.

| Variable | Default | Description |
| --- | --- | --- |
| `ADDR` | `:8080` | HTTP listen address |
| `DB_PATH` | `data/vartalap.db` | SQLite database file (created automatically) |
| `DATA_DIR` | `data` | Directory for encrypted attachment blobs and public avatars |
| `APP_ENV` | `development` | Deployment label (`development` or `production`) |
| `ALLOWED_ORIGINS` | empty | Comma-separated extra WebSocket origins; same-origin is always accepted |
| `MAX_ATTACHMENT_BYTES` | `10485760` (10 MiB) | Maximum encrypted attachment size stored by the server |
| `MAX_AVATAR_BYTES` | `524288` (512 KiB) | Maximum public avatar upload |
| `MESSAGE_RETENTION_DAYS` | `30` | How long undelivered ciphertext may remain queued |
| `ATTACHMENT_RETENTION_DAYS` | `7` | How long encrypted attachment blobs are kept |
| `MAX_DEVICES_PER_USER` | `1` | Reserved for the multi-device design; this release supports one active device |
| `RATE_PER_SECOND` | `2` | Generic token-bucket refill rate |
| `RATE_BURST` | `5` | Generic token-bucket burst |
| `SHUTDOWN_TIMEOUT_SECONDS` | `10` | Graceful shutdown deadline |

`HISTORY_LIMIT`, `MAX_MESSAGE_RUNES` and `MAX_MESSAGE_BYTES` are retained for
configuration compatibility. The server cannot inspect plaintext, so message
length is enforced by the client (`4000` characters) and the WebSocket frame
limit (`64 KiB`).

## How a message flows

```
Browser A                    Vartalap server                    Browser B
─────────                    ───────────────                    ─────────
encrypt(payload)  ──POST──▶  validate + store ciphertext  ──WS──▶  verify sig
                              queue per recipient device           decrypt
                                                                   save to IndexedDB
                              delete ciphertext  ◀──ack───────────  acknowledge
```

1. The sender fetches the recipient's public key bundle.
2. The browser creates an ephemeral ECDH key, derives an AES-256-GCM key with
   HKDF, encrypts the JSON payload, and signs the canonical envelope with its
   ECDSA key.
3. The server validates the envelope shape, that the sender matches the
   session, that the recipient is the conversation peer, and the signature.
   It then stores the ciphertext and queues delivery.
4. The recipient receives the envelope over WebSocket (or drains
   `/api/messages/pending`), verifies the signature, decrypts it, stores it in
   IndexedDB, and sends an acknowledgement.
5. Once every recipient device acknowledges, the server deletes the envelope.
   Local device history remains.

See [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) for the full lifecycle.

## Project layout

```
cmd/server            Composition root, signal handling, retention janitor
internal/config       Environment configuration
internal/models       Shared value types and JSON shapes
internal/repository   SQLite persistence and migrations runner
internal/services     IDs, usernames, crypto validation, tokens, rate limits, files
internal/websocket    Authenticated hub, presence, typing, client pumps
internal/render       Template parsing and fragment rendering
internal/handlers     HTTP pages, JSON API, attachments, WebSocket upgrade
internal/middleware   Signed device identity, CSRF, security headers, gzip, logging
migrations            Embedded, versioned SQL migrations
templates             Embedded layouts, pages and components
static                Embedded Carbon CSS, fonts, logos, HTMX and client JS
scripts               Client-crypto smoke test
docs                  Architecture, security, API, database, development, ADRs
```

## Testing

```bash
make test        # go test ./... -count=1
make test-js     # node scripts/test-crypto.mjs
make build
```

The Go suite covers username and key validation, canonical transcripts,
signature verification, envelope validation, the full HTTP identity →
conversation → message → ack flow, recovery and device rebinding, attachment
membership, WebSocket delivery/ack/typing, origin checks, migrations, retention
and rendering. The Node script exercises the browser module with Node's
WebCrypto: identity generation, BIP-39 phrase validation, backup
encrypt/decrypt, message encrypt/sign/verify/decrypt, wrong-key rejection,
tamper rejection and attachment encryption.

## Production checklist

1. Run behind HTTPS. WebCrypto will not work otherwise, and cookies are marked
   `Secure` only when the request is actually HTTPS (directly or via
   `X-Forwarded-Proto: https`).
2. Set `ALLOWED_ORIGINS=https://your-domain.example` when a proxy changes the
   host, and forward `Origin`, `Upgrade` and `Connection` headers for `/ws`.
3. Run as a non-root user with a writable `data/` directory.
4. Keep `data/vartalap.db` across deploys: it holds the server token-signing
   secret. Back up the database and `data/attachments/` together.
5. Run a single application instance. The WebSocket hub and rate limiter are
   in-memory, and SQLite is opened with one writer connection.
6. Terminate and restart gracefully; `SIGTERM`/`SIGINT` trigger shutdown.

Example systemd unit:

```ini
[Service]
WorkingDirectory=/srv/vartalap
ExecStart=/srv/vartalap/bin/vartalap
Environment=APP_ENV=production
Environment=ADDR=127.0.0.1:8080
Environment=ALLOWED_ORIGINS=https://chat.example.com
Restart=on-failure
NoNewPrivileges=true
```

## Security summary

- Private keys never leave the browser. They are stored as non-extractable
  `CryptoKey` objects in IndexedDB; the recovery JWKs are exported once at
  creation to build the encrypted backup.
- The server stores public ECDH/ECDSA keys, ciphertext, delivery state and
  opaque attachment metadata. It does not store plaintext, private keys,
  recovery phrases or attachment keys.
- The signed device cookie is **transport/session authentication only**, not a
  cryptographic identity. Anyone who steals it can act as that browser session
  until the device is revoked or the identity is recovered.
- There is no forward secrecy, no post-compromise security and no replay
  protection after a message is acknowledged and deleted. The sender's ECDSA
  signature is non-repudiable.
- Metadata is visible to the server: usernames, conversation pairs, message
  timing and ciphertext sizes, attachment sizes, delivery state and network
  addresses.
- Avatars are public and unencrypted.
- XSS in the origin would defeat the scheme; the CSP is strict and scripts are
  same-origin only, but this remains a single-origin trust boundary.

Read [docs/SECURITY.md](docs/SECURITY.md) for the full threat model, wire
formats and limitations.

## Documentation

- [docs/README.md](docs/README.md) — documentation index
- [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) — components and flows
- [docs/SECURITY.md](docs/SECURITY.md) — cryptography and threat model
- [docs/API.md](docs/API.md) — HTTP and WebSocket reference
- [docs/DATABASE.md](docs/DATABASE.md) — schema and migrations
- [docs/DEVELOPMENT.md](docs/DEVELOPMENT.md) — local development and debugging
- [docs/IMPLEMENTATION_STATUS.md](docs/IMPLEMENTATION_STATUS.md) — what is built
- [docs/CODEBASE_MAP.md](docs/CODEBASE_MAP.md) — where things live
- [AGENTS.md](AGENTS.md) — contributor and AI-agent rules

## License

MIT. Third-party assets and their licenses are listed in
[THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md).
