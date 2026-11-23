# ADR-002: WebCrypto sealed envelope instead of the Signal Protocol

- **Status:** Accepted
- **Date:** 2026-11-23

## Context

The product needs end-to-end encryption in the browser with a small, auditable
codebase. The Signal Protocol (X3DH/PQXDH + Double Ratchet) is the strongest
widely deployed design, but it requires a substantial protocol implementation,
prekey management, session state and careful ratchet handling.

Constraints:

- The stack is Go + vanilla browser JavaScript, no build step and no npm.
- The project is MIT-licensed and must not take on GPL-3.0 obligations.
- The priority is a scheme a reviewer can understand end to end.
- The official `@signalapp/libsignal-client` is Node-only and does not run in a
  plain browser without a bundler and WASM/Node shims.
- Browser Signal implementations that were evaluated carry GPL-3.0 licensing.

## Decision

Use a deliberately simple **sealed envelope** scheme built only from WebCrypto
primitives:

1. Generate a fresh ephemeral ECDH P-256 key pair per message.
2. Perform ECDH with the recipient's static P-256 public key.
3. Derive a 256-bit key with HKDF-SHA-256, salted with
   `vartalap:message-salt:v1` and bound to the conversation and message IDs.
4. Encrypt the JSON payload with AES-256-GCM (12-byte IV, 128-bit tag).
5. Sign the canonical envelope with the sender's static ECDSA P-256 key.

The server verifies the signature and envelope binding but never derives keys or
decrypts.

This decision explicitly accepts the loss of:

- forward secrecy;
- post-compromise security;
- key erasure and ratcheting;
- asynchronous prekey sessions.

These limitations are documented in [`../SECURITY.md`](../SECURITY.md).

## Consequences

Positive:

- Small, understandable implementation using vetted browser primitives.
- No third-party crypto dependency and no GPL obligation.
- Static recipient keys are simple to publish in a key bundle.
- The server can validate authorship without seeing plaintext.

Negative:

- Compromise of a recipient's static private key decrypts every captured
  message addressed to it.
- There is no healing after compromise.
- Signatures are non-repudiable, so there is no cryptographic deniability.
- The scheme is not suitable for high-risk threat models that require Signal's
  properties.

## Deferred work

A future version may adopt a vetted ratchet. That would require:

- a new message domain (`vartalap:message:v2`);
- session/prekey state and migration;
- dual-read/dual-write or a coordinated cutover;
- a fresh security review and updated docs.

PQXDH and the Signal Protocol remain the recommended direction if the project's
threat model ever demands forward secrecy and post-compromise security.

## Alternatives considered

- **Signal Protocol via `@signalapp/libsignal-client`.** Rejected: Node-only,
  needs a bundler/WASM shim, and conflicts with the no-build-step constraint.
- **Browser Signal libraries.** Rejected: GPL-3.0 obligations and heavier
  integration.
- **Age/HPKE-style single-shot encryption.** Viable, but the chosen ECDH +
  HKDF + AES-GCM construction is directly supported by WebCrypto and easier to
  test across the Go/JS boundary.
- **Server-side encryption.** Rejected: violates the core requirement that the
  server never sees plaintext.
