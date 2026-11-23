# Vartalap documentation

This directory is the reference for how Vartalap works and how to change it
safely. Start with the document that matches your task.

## Reading order

| If you want to… | Read |
| --- | --- |
| Understand the product and run it | [`../README.md`](../README.md) |
| Understand components and message flow | [`ARCHITECTURE.md`](ARCHITECTURE.md) |
| Understand cryptography and threats | [`SECURITY.md`](SECURITY.md) |
| Call the HTTP or WebSocket API | [`API.md`](API.md) |
| Inspect the schema and migrations | [`DATABASE.md`](DATABASE.md) |
| Set up a dev environment | [`DEVELOPMENT.md`](DEVELOPMENT.md) |
| See what is done and what is not | [`IMPLEMENTATION_STATUS.md`](IMPLEMENTATION_STATUS.md) |
| Find the file for a task | [`CODEBASE_MAP.md`](CODEBASE_MAP.md) |
| Understand a design decision | [`adr/`](adr/) |
| Contribute code or use an AI agent | [`../AGENTS.md`](../AGENTS.md) |

## One-paragraph summary

Vartalap is a Go server with an embedded SQLite database and an embedded
single-page browser client. Users register an anonymous identity by generating
an ECDH P-256 encryption key and an ECDSA P-256 signing key in the browser. The
server stores only the public halves, a username, a display name and an optional
public avatar. A conversation is a private 1:1 thread between two users.
Messages are encrypted and signed in the browser, sent as opaque envelopes over
HTTP, and delivered in real time over an authenticated WebSocket (or drained
later over HTTP). The recipient verifies, decrypts and stores each message in
IndexedDB, then acknowledges it; the server deletes the ciphertext once every
recipient device has acknowledged it. Attachments are separately encrypted with
a random AES key that travels inside the encrypted message. A twelve-word
recovery phrase unlocks a client-encrypted identity backup and, together with a
signature over a single-use challenge, rebinds a new browser to the identity.

## Key facts

| Property | Value |
| --- | --- |
| Language / runtime | Go 1.24+ (server), ES5-compatible browser JS (client) |
| Storage | SQLite (WAL) plus filesystem blobs |
| Transport | HTTP/JSON + WebSocket |
| UI | Go templates, HTMX, IBM Carbon CSS, no framework |
| Identity | Anonymous username; no email, phone or password |
| Message crypto | ECDH P-256 + HKDF-SHA-256 + AES-256-GCM |
| Signatures | ECDSA P-256 + SHA-256, raw `r\|\|s` |
| Recovery | BIP-39 12-word phrase + PBKDF2-SHA-256 + AES-256-GCM backup |
| Forward secrecy | **No** (documented limitation) |
| Multi-device | Schema-ready; one active device per identity in this release |
| Local history | IndexedDB, authoritative |
| Server message retention | 30 days or until all recipient devices ack |
| Server attachment retention | 7 days |

## Design decisions

The architecture decision records explain why the system looks the way it does,
including the important decision **not** to use the Signal Protocol in this
release:

- [ADR-001](adr/ADR-001-private-messaging-model.md) — private 1:1 instead of a public room
- [ADR-002](adr/ADR-002-webcrypto-sealed-envelope.md) — WebCrypto sealed envelope instead of Signal
- [ADR-003](adr/ADR-003-anonymous-identity-and-device-sessions.md) — anonymous identity and device sessions
- [ADR-004](adr/ADR-004-store-and-forward-with-client-history.md) — store-and-forward with client-authoritative history
- [ADR-005](adr/ADR-005-recovery-phrase-and-challenge.md) — phrase backup and signed recovery challenge
- [ADR-006](adr/ADR-006-encrypted-attachments.md) — encrypted attachments as opaque blobs
- [ADR-007](adr/ADR-007-versioned-migrations.md) — versioned embedded migrations
- [ADR-008](adr/ADR-008-indexeddb-and-single-device.md) — IndexedDB and one active device

## Documentation rules

- Documentation is part of the change. If code and docs disagree, that is a bug.
- Never document a security property the code does not provide.
- When you change a wire format, endpoint, default or limitation, update
  `API.md`, `SECURITY.md`, `DATABASE.md` or `DEVELOPMENT.md` as appropriate.
- Add a new ADR instead of rewriting the rationale of an accepted one.
