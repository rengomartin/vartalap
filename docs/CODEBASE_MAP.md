# Codebase map

A guide to where things live. Paths are relative to the repository root.

## Top level

```
cmd/server/          main: config, store, signer, hub, renderer, HTTP server, janitor
internal/            server packages (not importable outside this module)
migrations/          embedded versioned SQL
templates/           embedded Go html/template files
static/              embedded CSS, JS, fonts and SVG assets
scripts/             Node client-crypto smoke test
docs/                this documentation
data/                runtime database and blobs (gitignored)
bin/                 build output (gitignored)
```

## Server packages

### `cmd/server/main.go`

Composition root. Loads configuration, opens SQLite, applies migrations, loads
or creates the token-signing secret, prepares the file store, parses templates,
creates the WebSocket hub, builds the router, starts the HTTP server and runs
the hourly retention janitor. Handles `SIGINT`/`SIGTERM` with graceful shutdown.

### `internal/config/config.go`

Environment parsing and defaults. See [`DEVELOPMENT.md`](DEVELOPMENT.md) for the
full variable table. Note that `HistoryLimit`, `MaxMessageRunes` and
`MaxMessageBytes` are retained but not used for encrypted payloads.

### `internal/models/models.go`

Shared value types and JSON tags:

- `User`, `Summary`, `Device`, `KeyBundle`
- `Conversation`, `Envelope`, `MessageEvent`
- `Attachment`, `IdentityBackup`
- `PageData`, `DirectoryPage`

`Envelope` is the only message shape the server sees. Keep its JSON tags in sync
with `static/js/crypto.js` and `API.md`.

### `internal/services/`

| File | Responsibility |
| --- | --- |
| `ids.go` | UUID v4 and random hex tokens |
| `identity.go` | Device ID rules, display-name and device-name validation, cookie name |
| `username.go` | Username normalization, validation and reserved names |
| `crypto.go` | Key/signature decoding, canonical transcripts, fingerprint, envelope validation |
| `token.go` | HMAC-signed device tokens (`v1.<uuid>.<mac>`) |
| `ratelimit.go` | In-memory token-bucket limiter |
| `storage.go` | Filesystem store for attachment ciphertext and avatars |

`crypto.go` and `static/js/crypto.js` are a matched pair. Change them together.

### `internal/repository/`

| File | Responsibility |
| --- | --- |
| `sqlite.go` | Open, WAL/foreign-key settings, migration runner, secret loading |
| `users.go` | Accounts, profile updates, directory listing, search |
| `devices.go` | Device lookup, active device, revocation, rebinding |
| `conversations.go` | Pair-unique conversations, membership, partner lookup |
| `messages.go` | Enqueue envelope, pending list, delivery, ack/delete, expiry |
| `attachments.go` | Opaque attachment metadata and expiry |
| `backups.go` | Client-encrypted identity backup upsert/read |
| `recovery.go` | Single-use recovery challenges |
| `secrets.go` | Persistent server secret (`app_secrets`) |

All SQL is parameterized. `Store` opens SQLite with one connection so writes are
serialized and `SQLITE_BUSY` is avoided.

### `internal/websocket/`

| File | Responsibility |
| --- | --- |
| `hub.go` | Connection registry, presence fan-out, per-user routing, online partners |
| `client.go` | Per-connection read/write pumps, ping/pong, ack and typing handling |

The hub is in-memory and process-local. It routes ciphertext only. A user may
have up to five open sockets; a slow client is closed rather than blocking
routing. The send queue is 512 events so a backlog of pending offline messages
can be enqueued before the writer starts.

### `internal/handlers/`

| File | Responsibility |
| --- | --- |
| `handlers.go` | Router, dependencies, per-bucket limiters, JSON/HTML helpers, static assets |
| `pages.go` | `/`, `/u/{username}`, `/avatar/{id}`, discovery fragments |
| `api_identity.go` | Register, me, profile, avatar, forget, recovery start/complete |
| `api_discovery.go` | Directory, search, user lookup, key bundle |
| `api_conversations.go` | Create/list/get conversation, membership helper |
| `api_messages.go` | Send, pending, ack, delete |
| `api_attachments.go` | Upload/download opaque attachment ciphertext |
| `ws.go` | Authenticated WebSocket upgrade, origin check, pending flush |

Routing order matters: `/api/identity/register` and the recovery endpoints are
outside the authenticated `/api` group because they run before an identity
exists. Everything else under `/api` requires a valid device session.

### `internal/middleware/`

| File | Responsibility |
| --- | --- |
| `identity.go` | Resolve/repair signed device cookies and the `X-Vartalap-Device` header |
| `csrf.go` | Double-submit CSRF token and verification |
| `http.go` | Security headers/CSP, request logging, gzip for assets |

### `internal/render/render.go`

Parses embedded layouts, pages and components. Renders full pages and named
fragments (`discover-results`). Provides template helpers (`initial`,
`avatarURL`, `hasAvatar`, `formatTime`, `rfc3339`, `lower`).

## Client

### `static/js/`

| File | Responsibility |
| --- | --- |
| `crypto.js` | All WebCrypto operations, canonical transcripts, BIP-39, backup, attachments |
| `db.js` | IndexedDB schema and helpers (`identity`, `contacts`, `conversations`, `messages`, `outbox`, `settings`) |
| `app.js` | Messenger controller: boot, conversations, send/receive, outbox, acks, WS, presence, typing, UI rendering |
| `onboarding.js` | Create identity, show phrase, restore identity |
| `profile.js` | Public profile page: theme toggle and copy link |
| `ui.js` | Toasts, theme, `fetch` wrapper with CSRF, formatting, clipboard |
| `theme.js` | Pre-paint light/dark theme selection |
| `bip39-wordlist.js` | 2048-word BIP-39 English list |
| `htmx.min.js` | Vendored HTMX for discovery fragments |

`app.js` is the largest file and is organized by sections: state, API helpers,
conversations, messages, reply/attachment UI, presence/typing, WebSocket,
pending/outbox, settings/verification, event wiring, boot.

### `templates/`

```
layouts/base.html          document shell, meta tags, script selection
pages/onboarding.html      create/restore identity, recovery phrase dialog
pages/app.html             messenger shell, settings and verification dialogs
pages/profile.html         public profile
pages/notfound.html        404
components/discover.html   directory/search fragment used by HTMX
```

`base.html` loads different scripts per `PageData.View` (`onboarding`, `app`,
`profile`). Do not load `app.js` on the onboarding page or vice versa.

### `static/css/`

- `carbon.css` — vendored Carbon design system.
- `app.css` — all application layout and component styling, including
  onboarding, messenger, profile, dialogs, toasts and responsive rules.

## Migrations

```
001_init.sql        legacy public-chat tables (preserved, unused)
002_identity.sql    server-held secrets
003_messenger.sql   users, devices, conversations, messages, attachments, backups, recovery
```

The runner is in `internal/repository/sqlite.go`; embedding is in
`migrations/migrations.go`. See [`DATABASE.md`](DATABASE.md).

## Scripts

- `scripts/test-crypto.mjs` — loads `bip39-wordlist.js` and `crypto.js` into
  Node with a minimal `window` shim and asserts the exact wire formats the Go
  server verifies. Run with `make test-js`.

## Task → file lookup

| I need to… | Look at |
| --- | --- |
| Add a field to a message payload | `static/js/app.js` (payload), `crypto.js` (unchanged if inside payload), `app.js` (render) |
| Add a new API endpoint | `handlers.go` route, new handler, `API.md` |
| Change the signature transcript | `services/crypto.go`, `crypto.js`, tests |
| Change a table | new migration, repository file, `DATABASE.md` |
| Add a rate-limit bucket | `handlers.go` `limiters` map |
| Change CSP or cookies | `middleware/http.go` or `identity.go`, `SECURITY.md` |
| Add a settings field | `templates/pages/app.html`, `app.js`, `onboarding.js`, DB if persisted |
| Change discovery | `api_discovery.go`, `pages.go`, `components/discover.html`, `app.css` |
| Change retention | `config.go`, `main.go` janitor, `DATABASE.md` |
| Debug a crypto mismatch | `scripts/test-crypto.mjs`, `services/crypto_test.go`, canonical functions |
