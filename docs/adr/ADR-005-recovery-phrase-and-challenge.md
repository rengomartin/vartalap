# ADR-005: Recovery phrase and signed challenge

- **Status:** Accepted
- **Date:** 2026-11-23

## Context

There is no email or password reset. If a browser loses its IndexedDB keys, the
identity is otherwise unrecoverable. Users need a way to move to a new browser
without giving the server the ability to impersonate them.

## Decision

Use a twelve-word BIP-39 recovery phrase that encrypts the identity private keys
into a server-stored backup, combined with a signature over a single-use
challenge.

### Backup creation

At registration the browser:

1. generates 128 bits of entropy and encodes it as 12 BIP-39 words;
2. derives a key with PBKDF2-SHA-256 (210,000 iterations, 16-byte random salt);
3. encrypts a JSON document containing the private JWKs and public keys with
   AES-256-GCM (12-byte IV);
4. sends only `ciphertext`, `iv`, `kdfSalt` and `kdfIterations` to the server.

The phrase never leaves the browser and is shown once with copy/download and a
confirmation gate.

### Recovery

1. `POST /api/identity/recovery/start {username}` returns the encrypted backup,
   the public keys and a random 32-byte challenge valid for five minutes.
2. The browser decrypts the backup with the phrase, imports the keys, and checks
   that the derived public keys match the response.
3. The browser signs:

   ```
   vartalap:recover:v1
   <userId>
   <newDeviceId>
   <challenge>
   <timestamp>
   ```

   with the old signing key.
4. `POST /api/identity/recovery/complete` consumes the challenge atomically,
   verifies the signature against the old device's registered signing key,
   revokes all existing devices and binds the new transport device to the
   identity.

## Consequences

- The server cannot decrypt the backup and cannot recover an identity without
  the phrase.
- Possession of the phrase is effectively possession of the identity, because it
  unlocks the signing key.
- Recovery restores keys, not message history.
- Recovery revokes the previous device; the old browser is signed out.
- The challenge is consumed before signature verification, so an invalid attempt
  burns it. The user must request a new challenge. This is a simplicity
  trade-off and is rate-limited.
- `recovery/start` exposes the encrypted backup to anyone who knows the
  username. The backup's strength depends on the generated phrase and PBKDF2.
  Users must not substitute a low-entropy phrase.
- The backup is created at registration and can be updated via the repository,
  but there is no UI to rotate the phrase yet.

## Alternatives considered

- **Server-held recovery key.** Rejected: the server could impersonate users.
- **Social recovery / shards.** Rejected for this release: more moving parts and
  new metadata.
- **No recovery.** Rejected: unacceptable UX; a lost browser would permanently
  lose the identity.
- **Phrase alone rebinds the device.** Rejected: the server would accept a
  bearer secret over the network. Requiring a signature proves possession of the
  old signing key and prevents a stolen backup from being rebound without
  decrypting it.
