# ADR-003: Anonymous identity and device sessions

- **Status:** Accepted
- **Date:** 2026-11-23

## Context

Users should be able to start chatting without an email address, phone number or
password, while still having a stable, unique, shareable identity. The server
must not become a password store, and it must not be able to impersonate users
cryptographically.

## Decision

Separate **cryptographic identity** from **transport session**.

### Cryptographic identity

- Generated in the browser: ECDH P-256 (encryption) and ECDSA P-256 (signing).
- The server stores only the public keys in `devices`.
- Private keys are stored as non-extractable `CryptoKey` objects in IndexedDB.
- Registration proves possession by signing a canonical transcript containing
  the device ID, username, display name, public keys and a timestamp.
- Usernames are unique, case-insensitive, 3–24 characters of `[a-z0-9_]`, with
  reserved names blocked.
- Display names are 1–48 runes; a public avatar is optional.

### Transport session

- An HMAC-signed token `v1.<deviceUUID>.<mac>` signed with a 32-byte secret in
  `app_secrets`.
- Stored in `vartalap_id` (HttpOnly) and `vartalap_sync` (JS-readable) cookies,
  with an `X-Vartalap-Device` header fallback.
- Cookies last 400 days, use `SameSite=Lax`, and are marked `Secure` on HTTPS.
- Missing or invalid tokens get a fresh device ID; a surviving copy repairs the
  other.

The session authenticates HTTP and WebSocket requests. It is **not** the
cryptographic identity: without the private keys, a stolen cookie cannot decrypt
messages or forge a signed envelope.

## Consequences

- No passwords, password resets, email verification or phone numbers.
- Users can create multiple identities; there is no uniqueness of person.
- The device cookie is a bearer credential for metadata and destructive actions,
  so it must be protected like a password.
- Losing the browser means losing the identity unless a recovery phrase was
  saved.
- Device tokens survive restarts because the signing secret is persisted.
- Rotating the secret logs out every session.

## Alternatives considered

- **Email/password accounts.** Rejected: adds personal data, reset flows and a
  password database, contradicting the anonymous design.
- **WebAuthn/passkeys.** Rejected for this release: good authentication, but
  still requires a server-side account model and does not solve key recovery.
- **Use the cryptographic key as the session credential.** Rejected: signing
  every request is more complex, and the transport session already separates
  routing from identity. The signature on each envelope is the real authorship
  proof.
- **Unsigned device cookies.** Rejected: a UUID cookie is trivially forgeable.
