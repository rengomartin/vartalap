# Development

## Prerequisites

- **Go 1.24+**.
- A **C compiler** (`gcc` or `clang`) because `github.com/mattn/go-sqlite3` uses
  cgo.
- **Node 20+** (optional) for the client-crypto smoke test.
- A modern browser with WebCrypto and IndexedDB.

No npm, bundler or frontend build step is required.

## Run locally

```bash
go run ./cmd/server
```

Open <http://localhost:5000>. `localhost` counts as a secure context, so
WebCrypto works over plain HTTP there. On any other host you must use HTTPS or
the onboarding page will block with an explanatory error.

Build:

```bash
make build          # bin/vartalap
./bin/vartalap
```

With explicit settings:

```bash
ADDR=:3000 DB_PATH=./data/dev.db DATA_DIR=./data \
APP_ENV=development ALLOWED_ORIGINS=http://localhost:3000 \
go run ./cmd/server
```

## Configuration

See the table in [`../README.md`](../README.md). Notable points:

- `DB_PATH` and `DATA_DIR` are separate. Keep attachment/avatar files next to
  the database you back up.
- `ALLOWED_ORIGINS` is only needed when a proxy changes the host or you connect
  from a different origin. Same-origin is always allowed.
- `MESSAGE_RETENTION_DAYS` and `ATTACHMENT_RETENTION_DAYS` control the janitor.
- `MAX_ATTACHMENT_BYTES` is the server limit; the client independently caps
  attachments at 8 MiB.
- `HISTORY_LIMIT`, `MAX_MESSAGE_RUNES` and `MAX_MESSAGE_BYTES` are retained for
  compatibility but do not apply to encrypted payloads.

## Make targets

```bash
make build     # compile the server binary
make run       # go run ./cmd/server
make test      # go test ./... -count=1
make test-js   # node scripts/test-crypto.mjs
make tidy      # go mod tidy
```

## Testing

### Go

```bash
gofmt -l .                 # must print nothing
go vet ./...
go test ./... -count=1
```

Coverage by package:

- `internal/services` — UUIDs, usernames, display/device names, signed tokens,
  limiter, key/signature decoding, canonical transcripts, fingerprints,
  envelope validation.
- `internal/repository` — account creation and username uniqueness, pair-unique
  conversations, delivery lifecycle, sender delete rules, expiry, single-use
  recovery challenges, device rebinding.
- `internal/middleware` — cookie issuance/repair, forged-token rejection,
  HTTPS-only `Secure`, CSRF behavior.
- `internal/handlers` — registration, profile, discovery, conversation,
  send/ack/delete, recovery and revocation, attachments and membership,
  WebSocket delivery/ack/typing, origin and auth rejection.
- `internal/render` — every page and the discovery fragment render.
- `migrations` — exercised through the repository tests.

Tests use real HTTP servers, real cookies, real CSRF headers and real WebSocket
connections. There are no crypto mocks; the tests generate P-256 keys with the
Go standard library.

### Client crypto

```bash
node scripts/test-crypto.mjs
# or
make test-js
```

The script shims `window`, `btoa`/`atob` and a minimal `document`, then loads
`bip39-wordlist.js` and `crypto.js`. It checks:

- identity generation and 65-byte public key encoding;
- BIP-39 phrase generation and checksum validation;
- backup encrypt/decrypt and identity re-import;
- message encrypt → sign → verify → decrypt;
- wrong-recipient decryption failure;
- AES-GCM tamper rejection;
- attachment encrypt/decrypt round trip.

If you change any wire format, update both this script and the Go tests.

## Working on the frontend

Assets and templates are **embedded in the binary**. Editing a template or a
`static/` file requires restarting `go run ./cmd/server`; there is no hot reload.

Typical loop:

1. Edit `static/js/*.js`, `static/css/app.css`, `templates/**`.
2. Restart the server.
3. Hard-reload the browser (disable cache) to bypass the one-hour static cache.

Client code style:

- ES5-compatible IIFEs attached to `window.VartalapCrypto`, `VartalapDB`,
  `VartalapUI`.
- No bundler, no imports, no template literals in shipped code.
- Keep private keys as `CryptoKey` objects; never write raw private JWKs to
  IndexedDB.
- Use `UI.api()` for JSON requests so CSRF and error handling stay consistent.
- Use `textContent` for user data; avoid `innerHTML` except for fixed SVG icons.

### Local storage

IndexedDB database `vartalap`, version 1, stores:

| Store | Key | Contents |
| --- | --- | --- |
| `identity` | `id = "self"` | user/device IDs, public keys, non-extractable private keys, fingerprint |
| `contacts` | `userId` | public keys, fingerprint, verified/identityChanged flags |
| `conversations` | `id` | peer summary, preview, unread, timestamps |
| `messages` | `id` | plaintext history, attachment metadata, direction, state |
| `outbox` | `id` | encrypted envelope awaiting delivery |
| `settings` | `key` | small preferences |

To change the schema, bump `DB_VERSION` in `static/js/db.js` and add an
`onupgradeneeded` migration. Never assume existing users have a clean store.

To reset a browser: DevTools → Application → Storage → delete the `vartalap`
IndexedDB database and the site cookies, then reload. This destroys the local
private keys; recover with the phrase if you need the identity back.

## Testing the full flow manually

Use two separate browser profiles or one normal and one private window (not two
tabs in the same profile, because they share IndexedDB and cookies).

1. Profile A: create an identity, save the recovery phrase.
2. Profile B: create a different identity.
3. From A, search for B and start a chat; send a message.
4. Observe B receive it over WebSocket and A receive a `delivery` update after
   B's client acks.
5. Close B, send more messages from A, reopen B: pending messages arrive.
6. Verify the contact fingerprint from the shield button in the chat header.
7. Upload an attachment, then download/decrypt it on the other side.
8. Reply to a message and check the quoted preview.
9. Send typing indicators and watch presence change.
10. Use "Forget this device" on B, then restore with the phrase in a fresh
    profile and confirm the old session is revoked.

Recovery test without a second profile: copy the phrase, use "Forget this
device", reload, choose "Restore with phrase", and enter the username and
phrase.

## Debugging

### Server logs

The request logger prints method, URI, status and duration. Handler errors are
logged with details while clients receive generic messages. Message bodies and
ciphertext are never logged by the application.

### Browser

- Console: crypto errors, WebSocket state, API errors.
- Network: inspect `/api/*` payloads; ciphertext is expected to look random.
- Application → IndexedDB → `vartalap`: inspect identity, contacts, messages.
- Application → Cookies: `vartalap_id`, `vartalap_sync`, `vartalap_csrf`.
- WebSocket frames: DevTools → Network → WS → Messages.

### Common problems

| Symptom | Cause / fix |
| --- | --- |
| "needs a secure context" | Use `localhost` or HTTPS. WebCrypto is unavailable on plain HTTP elsewhere. |
| `CGO_ENABLED=0` build fails | The SQLite driver needs cgo. Install a C compiler. |
| 401 after recovery | The old device was revoked by design. Clear cookies/local data and restore, or use the new device. |
| "This browser is signed into a different session" | Cookies and IndexedDB identity disagree. Clear local data and restore with the phrase. |
| Username rejected | 3–24 chars, `[a-z0-9_]`, no leading/trailing/double underscore, not reserved. |
| Attachment download 404 | Blob expired (7 days) or was never uploaded; metadata may still exist. |
| Attachment 415 | Send `Content-Type: application/octet-stream` or omit it. |
| WebSocket 403 | Origin mismatch or no active identity. Check the proxy forwards `Origin`. |
| Cookies not persisting | Plain HTTP on a non-local host, or a proxy not forwarding `X-Forwarded-Proto`. |
| Duplicate messages after reconnect | Should be deduplicated by ID; check IndexedDB and the client dedup path. |
| Message stuck "sending" | Inspect the outbox store and the send response; retry from the UI. |

## Adding an endpoint

1. Add the route in `internal/handlers/handlers.go` (inside `api.Use(h.requireUser)`
   for authenticated routes).
2. Implement the handler in the appropriate `api_*.go` file.
3. Check CSRF for state-changing requests (`h.csrfOK`).
4. Check rate limits with `h.allow("bucket", key)`.
5. Validate input with `decodeJSON` and the services validators.
6. Never accept identity fields from the body; derive them from the session.
7. Add an HTTP test in `internal/handlers/handlers_test.go`.
8. Document the endpoint in [`API.md`](API.md).

## Adding a migration

```bash
# next version after the highest existing number
$EDITOR migrations/004_something.sql
```

Then add repository code, tests, and update [`DATABASE.md`](DATABASE.md). Never
edit an applied migration. Test both a fresh database and an upgrade of an
existing `data/vartalap.db`.

## Changing cryptography

This is the highest-risk change in the project.

1. Decide whether the domain version must bump (`vartalap:*:v1`).
2. Change `internal/services/crypto.go` **and** `static/js/crypto.js` together.
3. Update `scripts/test-crypto.mjs` and `internal/services/crypto_test.go`.
4. Consider existing clients and stored data. A hard cutover breaks old
   clients; a version bump needs dual-read/dual-write or a migration plan.
5. Update [`SECURITY.md`](SECURITY.md) and add an ADR if the properties change.
6. Run the full suite and manually test registration, messaging and recovery.

## Code review checklist

- [ ] Server never receives plaintext, private keys, phrases or attachment keys.
- [ ] Sender identity comes from the session, not the request body.
- [ ] Recipient is the conversation peer; membership is checked.
- [ ] Envelope signature is verified before storage.
- [ ] CSRF on every state-changing route.
- [ ] Input validated and bounded; SQL parameterized.
- [ ] Errors are generic to clients, detailed in logs, and contain no secrets.
- [ ] Client and server transcripts still match.
- [ ] Tests added or updated.
- [ ] Docs updated (`API.md`, `SECURITY.md`, `DATABASE.md` as applicable).
- [ ] `gofmt -l .`, `go vet ./...`, `go test ./...`, `node scripts/test-crypto.mjs`
      all clean.

## Release checklist

1. `gofmt -l .` empty, `go vet ./...` clean, `go test ./... -count=1` passes.
2. `node scripts/test-crypto.mjs` passes.
3. `make build` succeeds.
4. Manual smoke test in two browser profiles over HTTPS or localhost.
5. Confirm `docs/IMPLEMENTATION_STATUS.md` matches reality.
6. Back up the production database and attachment directory before deploying.
7. Deploy one instance; do not horizontally scale without redesigning the hub
   and limiter.
