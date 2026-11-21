# Vartalap

A minimal, anonymous, real-time public chat room. No accounts, no passwords — pick a name once and this device is remembered.

Built with Go, WebSockets, SQLite, server-rendered Go templates, HTMX (including its WebSocket extension) and the **IBM Carbon Design System**. The UI uses Carbon tokens, typography, buttons, inputs, tags and notifications. No frontend framework, no build step for the UI, and no runtime CDN dependency: Carbon CSS, IBM Plex fonts, logos, templates, migrations and JS are all embedded in the Go binary.

## Features

- One default public room for everybody.
- **Automatic sign-in on return visits.** A signed, HMAC-protected device token is stored in an `HttpOnly` cookie (400 days), mirrored in a JavaScript-readable sync cookie and in `localStorage`. Losing one copy is repaired from the others, and `POST /restore` re-establishes the session if every cookie disappeared.
- Display name on first visit, editable at any time. It is restored automatically after closing and reopening the site.
- Carbon White / Gray-100 themes with a persisted light/dark toggle.
- Branded SVG logo, mark and favicon.
- "Device and session" dialog showing how the current device is remembered, with a "Forget this device" action.
- Real-time message delivery over WebSockets with automatic reconnection (HTMX WS extension).
- Latest 50 messages on join; older messages load automatically when scrolling up.
- Sender names, subtle timestamps, consecutive-message grouping, own messages highlighted with a Carbon blue accent.
- Online count of **distinct devices**, updated on connect/disconnect; multiple tabs from one device count once.
- Server-side validation, rate limiting, HTML escaping, parameterized SQL, WebSocket origin checks.
- Mobile-first, full-height layout with a fixed composer and safe-area support.
- Graceful shutdown and WAL-mode SQLite persistence.

## Quick start

Requires Go 1.24+ and a C compiler (for the SQLite driver).

```bash
go run ./cmd/server
```

Open <http://localhost:8080>. Choose a name and start chatting.

To build and run a binary:

```bash
make build
./bin/vartalap
```

Or with explicit settings:

```bash
ADDR=:3000 DB_PATH=./data/chat.db APP_ENV=development go run ./cmd/server
```

## Configuration

All settings are environment variables with sensible defaults.

| Variable | Default | Description |
| --- | --- | --- |
| `ADDR` | `:8080` | HTTP listen address |
| `DB_PATH` | `data/vartalap.db` | SQLite database file (created automatically) |
| `APP_ENV` | `development` | Deployment label used in logs (`development` or `production`) |
| `ALLOWED_ORIGINS` | empty | Comma-separated explicit WebSocket origin allow-list; same-origin is always allowed |
| `HISTORY_LIMIT` | `50` | Messages loaded on join and per history page (1–200) |
| `MAX_MESSAGE_RUNES` | `2000` | Maximum message length |
| `MAX_MESSAGE_BYTES` | `16384` | Maximum raw WebSocket frame size |
| `RATE_PER_SECOND` | `2` | Sustained messages per device per second |
| `RATE_BURST` | `5` | Initial message burst allowance |
| `SHUTDOWN_TIMEOUT_SECONDS` | `10` | Graceful shutdown deadline |

## Architecture

```
cmd/server/main.go          Composition root, signal handling, graceful shutdown
internal/config             Environment configuration
internal/models             Shared value types
internal/repository         SQLite schema access (users, messages, pagination)
internal/services           Device IDs, HMAC identity tokens, name/message validation, limiter
internal/websocket          In-memory hub, connection registry, presence, client pumps
internal/render             Go html/template parsing and HTML fragment rendering
internal/handlers           HTTP routes, pages, history API, WebSocket upgrade
internal/middleware         Signed device identity, security headers, logging, gzip
migrations                  Embedded SQL schema
templates                   Embedded layouts, pages and components
static                      Embedded Carbon CSS, IBM Plex fonts, logos, htmx, WS extension, app JS
```

Request flow:

1. `GET /` verifies the signed device token (primary cookie, then sync cookie, then the `X-Vartalap-Device` header). Without a stored name it renders the welcome screen; otherwise it renders the chat page with the latest messages.
2. `POST /join` validates and stores the display name, then returns a fresh chat document. A normal form post avoids duplicate element IDs in the live chat view.
3. The chat view carries `hx-ext="ws" ws-connect="/ws"`. Incoming WebSocket payloads are HTML fragments with `hx-swap-oob` attributes, which htmx swaps into the page.
4. The composer sends a JSON WebSocket frame through the socket wrapper captured by `app.js`. It renders an optimistic row immediately and reconciles it with the broadcast copy using a client-generated `nonce`. The server re-reads the display name from the database — client-supplied names are ignored.
5. Messages are persisted and broadcast. Each recipient gets a fragment rendered with their own `data-mine` flag. All messages are laid out left-to-right in a single transcript column, so your own lines stay on the left after a refresh too.
6. `GET /history?before=<id>` returns older messages plus a replacement "load older" sentinel.
7. If both cookies are missing but `localStorage` still holds a signed token, the welcome page posts it to `/restore` once. A valid token for an existing profile re-issues the cookies and redirects into the chat.
8. `POST /forget` expires both identity cookies; the client also clears its `localStorage` copy.

## Testing

```bash
make test
# or
go test ./... -count=1
```

Coverage includes device ID generation and validation, HMAC token issuing and tamper rejection, cookie repair from the sync mirror, `Secure` cookie selection for HTTP/HTTPS, restore and forget flows, display-name and message validation, user upserts, message persistence and pagination, grouping, rate limiting, HTTP onboarding/name editing/history/static assets, WebSocket connections, origin rejection, broadcasts, multi-tab presence, reconnection, malformed input and slow-client cleanup.

## Production deployment

1. Build a binary for your target platform and copy it next to a writable `data/` directory:
   ```bash
   CGO_ENABLED=1 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o bin/vartalap ./cmd/server
   ```
2. Run behind HTTPS (for example with Caddy or nginx). Cookies are marked `Secure` automatically whenever the request arrives over TLS or carries `X-Forwarded-Proto: https`; make sure the proxy forwards that header. Set `ALLOWED_ORIGINS=https://your-domain.example` when proxying. Ensure the proxy also forwards the `Origin` and `Upgrade`/`Connection` headers.
   Do not force `Secure` cookies on a plain-HTTP deployment: browsers silently discard them, which is the most common cause of a "remember me" cookie failing on every visit.
3. Run as a non-root user with a systemd service. The process handles `SIGTERM`/`SIGINT` and shuts down gracefully.
4. Back up `data/vartalap.db` (SQLite WAL mode is used; use `sqlite3 .backup` or copy the `-wal` and `-shm` files together).

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

## Security notes

Anonymous device IDs are **not** an authentication mechanism. Anyone who can copy a valid signed cookie can act as that device; this is accepted for a public, ephemeral chat. The server never trusts a client-provided sender name or device ID in a message payload — the name comes from the database and the device ID comes from a verified cookie token.

Device tokens are `v1.<uuid>.<HMAC-SHA256>` values signed with a 32-byte secret generated on first run and stored in the `app_secrets` table, so tokens survive restarts. A tampered UUID fails verification and gets a fresh identity instead of impersonating another device. Message markup contains only a short SHA-256 sender key (`data-sender`) for grouping; raw device IDs are never rendered. Glyph coverage is the Latin1 IBM Plex subset; other scripts fall back to the system font.

When upgrading from a build that used unsigned UUID cookies, those old cookies are deliberately not trusted. Each existing browser is asked for a display name one final time; after that the signed token is persisted and the name returns automatically. Keep `data/vartalap.db` across deploys — the signing key lives in that database.

## License

MIT
# vartalap
