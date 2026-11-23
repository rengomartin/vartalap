# AGENTS.md

Instructions for contributors and AI coding agents working on Vartalap. Read
this file before changing code. When in doubt, preserve the security invariants
below over feature convenience.

## What this project is

Vartalap is an anonymous, private, end-to-end encrypted 1:1 messenger built with
Go, SQLite, WebSockets, server-rendered Go templates, HTMX and IBM Carbon. The
server routes opaque ciphertext; the browser owns all plaintext and private
keys. There is no public chatroom.

## Non-negotiable security invariants

1. **The server must never receive plaintext message content, private keys,
   recovery phrases, attachment plaintext or attachment keys.** If a change
   would add any of these to a request, log, database column or error message,
   stop and redesign it.
2. **Do not invent cryptographic primitives.** Use the browser WebCrypto API
   (`ECDH` P-256, `ECDSA` P-256/SHA-256, `HKDF` SHA-256, `AES-GCM` 256,
   `PBKDF2` SHA-256). Do not add a bespoke cipher, hash or KDF.
3. **Client and server canonical transcripts must stay byte-identical.** They
   are newline-joined strings defined in `internal/services/crypto.go` and
   `static/js/crypto.js`. Changing either side without the other breaks
   registration, recovery or message verification. Bump the `:v1` domain and
   support a transition if the format must change.
4. **Do not modify an already-applied migration.** Add a new numbered file under
   `migrations/`. The runner records versions in `schema_migrations`; editing an
   applied file means deployed databases silently diverge.
5. **Never migrate old public-chat data into private conversations.** The legacy
   `anonymous_users` and `messages` tables are intentionally unused.
6. **Keep one conversation per unordered user pair.** `conversations(user_a,
   user_b)` is ordered with `user_a < user_b` and backed by a unique index.
7. **A message may only be deleted by its sender while no delivery has left the
   `queued` state.** Once a recipient has received it, deletion is local only.
8. **Recovery must prove possession of the old signing key.** The phrase unlocks
   the backup locally; the server verifies an ECDSA signature over a single-use
   challenge before rebinding a device.
9. **CSRF is mandatory on every state-changing endpoint.** Use the double-submit
   cookie (`vartalap_csrf` + `X-CSRF-Token`). Check the header before parsing
   form bodies so raw attachment uploads are not consumed.
10. **Do not trust client-supplied identity fields.** Derive the sender user and
    device from the signed session, verify the recipient is the conversation
    peer, and verify the envelope signature server-side.

## Build, test and format

```bash
gofmt -l .                 # must print nothing
go vet ./...               # must be clean
go test ./... -count=1     # must pass
node scripts/test-crypto.mjs
make build
```

`make test` runs the Go suite; `make test-js` runs the client-crypto smoke test.
If you change cryptography, add or update both a Go test and the Node smoke
test. If you change HTTP behavior, extend
`internal/handlers/handlers_test.go`.

## Where to change what

| Task | Files |
| --- | --- |
| Add or change an HTTP route | `internal/handlers/handlers.go` plus a focused `api_*.go` file |
| Change a JSON shape | `internal/models/models.go` and the matching JS caller |
| Change crypto wire format | `internal/services/crypto.go`, `static/js/crypto.js`, `scripts/test-crypto.mjs` |
| Change the schema | new `migrations/00N_*.sql`; never edit an applied file |
| Change SQL behavior | `internal/repository/*.go` |
| Change presence/typing/acks | `internal/websocket/*.go` and `static/js/app.js` |
| Change pages or fragments | `templates/**` and `internal/render/render.go` |
| Change styling | `static/css/app.css` (Carbon tokens live in `carbon.css`) |
| Change local storage | `static/js/db.js` and bump `DB_VERSION` with an upgrade path |
| Change configuration | `internal/config/config.go`, `README.md`, `docs/DEVELOPMENT.md` |
| Change security posture | `docs/SECURITY.md` and an ADR |

## Code style

- Go: standard library style, `gofmt`, small focused functions, explicit error
  handling, parameterized SQL only. Return generic client errors; log details.
- JavaScript: ES5-compatible browser modules attached to `window.Vartalap*`,
  `'use strict'`, no build step, no bundler, no framework. Avoid template
  literals and optional chaining in shipped client code.
- HTML: Go `html/template` auto-escaping; do not add inline scripts or styles
  that violate the CSP (`script-src 'self'`, `style-src 'self'`).
- CSS: use Carbon design tokens and existing class patterns in `app.css`.
- Docs: keep paths, endpoints, defaults and limitations synchronized with code.
  An inaccurate security doc is worse than no doc.

## Migrations

1. Create `migrations/00N_short_name.sql` with the next zero-padded version.
2. Use `CREATE TABLE IF NOT EXISTS` / `CREATE INDEX IF NOT EXISTS` where
   reasonable and keep the file self-contained.
3. Add repository code and tests.
4. Update `docs/DATABASE.md`.
5. Never rewrite history: a migration that has shipped is immutable.

## Testing expectations

- New repository query: a test in `internal/repository/repository_test.go`.
- New handler: an HTTP test with real cookies and CSRF headers in
  `internal/handlers/handlers_test.go`.
- New crypto transcript or validation: a Go test and a Node smoke-test check.
- New template or fragment: extend `internal/render/render_test.go`.
- Bug fix: add a regression test that fails before the fix.

## Common pitfalls

- **Secure context:** WebCrypto is unavailable on plain HTTP except localhost.
  The UI intentionally blocks with an explanatory error.
- **ECDSA encoding:** WebCrypto emits raw `r||s` (64 bytes). Go must split it into
  two 32-byte integers; do not expect ASN.1 DER.
- **Public keys:** base64 of the 65-byte uncompressed P-256 point (`0x04 || x ||
  y`). IVs are 12 bytes; signatures are 64 bytes.
- **Ack semantics:** "delivered" means the recipient verified, decrypted and
  stored the message and acknowledged it, not merely that a socket was open.
- **Post-ack replay:** after all devices ack, the server deletes the envelope and
  no longer remembers the ID. Clients deduplicate by message ID; the server's
  idempotency only holds while the row exists.
- **Legacy config:** `HISTORY_LIMIT`, `MAX_MESSAGE_RUNES` and `MAX_MESSAGE_BYTES`
  are retained but do not apply to encrypted payloads.
- **Single process:** the WebSocket hub, rate limiter and SQLite connection are
  process-local. Do not scale horizontally without redesigning them.
- **Avatars are public.** Never place them behind claims of E2EE.

## Documentation map

`README.md` (overview), `docs/README.md` (index), `docs/ARCHITECTURE.md`,
`docs/SECURITY.md`, `docs/DATABASE.md`, `docs/API.md`,
`docs/DEVELOPMENT.md`, `docs/IMPLEMENTATION_STATUS.md`,
`docs/CODEBASE_MAP.md`, and `docs/adr/ADR-001..008`.
