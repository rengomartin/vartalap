# ADR-001: Private 1:1 messaging instead of a public room

- **Status:** Accepted
- **Date:** 2026-11-23

## Context

Vartalap originally shipped a single public chat room: anyone who visited could
pick a display name and read every message. That model cannot provide
confidentiality. The product goal changed to an anonymous, private messenger
where every conversation is between exactly two people and the server cannot
read content.

## Decision

Replace the public room with private one-to-one conversations only.

- There is no global room and no group chat in this release.
- Every conversation is uniquely identified by an unordered pair of users.
- `conversations(user_a, user_b)` enforces `user_a < user_b` with a unique
  index, so concurrent creation converges and the pair cannot be duplicated by
  swapping order.
- `conversation_members` stores both participants and is checked on every
  message, attachment and typing operation.
- Old public-chat tables (`anonymous_users`, `messages`) are preserved but never
  read, and old plaintext messages are never imported into private
  conversations.

## Consequences

- The data model and UI are simpler: no room membership, invitations or group
  key management.
- Presence and typing fan out only to conversation partners.
- Adding groups later requires a new membership/key-distribution design and a
  new message domain; it is not a small extension of this model.
- Legacy plaintext remains in the database for compatibility but is
  inaccessible through the application. Operators may archive or delete it
  separately after taking their own migration decision.

## Alternatives considered

- **Keep the public room and add private DMs.** Rejected: it preserves a
  plaintext surface and complicates the security story.
- **Channels/servers with membership.** Rejected for the first release: group
  key distribution and membership churn are a much larger design.
- **Ephemeral rooms.** Rejected: offline delivery and local history require
  stable conversations.
