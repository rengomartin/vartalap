# Database

Vartalap uses SQLite in WAL mode with foreign keys enabled. The schema is
versioned, embedded in the binary and applied automatically at startup. All
timestamps are Unix milliseconds in UTC; booleans are stored as `0`/`1`.

## Opening the database

`internal/repository/sqlite.go`:

- creates the parent directory if needed;
- opens with `_busy_timeout=5000`, `_journal_mode=WAL`,
  `_synchronous=NORMAL`, `_foreign_keys=on`;
- sets `MaxOpenConns(1)` and `MaxIdleConns(1)` so writes are serialized and
  `SQLITE_BUSY` is avoided for this workload;
- accepts `:memory:` and `file:` DSNs for tests.

## Migrations

`Store.Migrate`:

1. creates `schema_migrations(version TEXT PRIMARY KEY, applied_at INTEGER)`;
2. reads all embedded `migrations/*.sql` sorted by filename;
3. for each version not present, executes the file and records the version in a
   transaction;
4. pings the database.

Rules:

- Filenames start with a zero-padded numeric version and an underscore.
- **Never edit an applied migration.** Add a new file.
- Migrations are idempotent where practical (`CREATE TABLE IF NOT EXISTS`) so
  databases created before the runner existed upgrade safely.

| Version | File | Purpose |
| --- | --- | --- |
| `001` | `001_init.sql` | Legacy public-chat tables, preserved and unused |
| `002` | `002_identity.sql` | Server-held secrets |
| `003` | `003_messenger.sql` | Private messenger schema |

## Legacy tables (preserved, unused)

### `anonymous_users`

| Column | Type | Notes |
| --- | --- | --- |
| `device_id` | TEXT | Primary key |
| `display_name` | TEXT | Old public name |
| `created_at` | INTEGER | ms |
| `updated_at` | INTEGER | ms |

### `messages`

| Column | Type | Notes |
| --- | --- | --- |
| `id` | INTEGER | Primary key, autoincrement |
| `device_id` | TEXT | Old sender device |
| `sender_name` | TEXT | Old public name |
| `content` | TEXT | **Old plaintext public messages** |
| `created_at` | INTEGER | ms |

Indexes: `idx_messages_id_desc`, `idx_messages_created_at_desc`.

These tables are intentionally untouched. Old public plaintext must never be
migrated into private conversations.

## Messenger tables

### `users`

| Column | Type | Notes |
| --- | --- | --- |
| `id` | TEXT | UUID v4 primary key |
| `username` | TEXT | Unique, `COLLATE NOCASE` |
| `display_name` | TEXT | 1–48 runes |
| `avatar_updated_at` | INTEGER | 0 means no avatar |
| `discoverable` | INTEGER | 1 listed in directory, 0 hidden |
| `primary_device_id` | TEXT | Current device UUID (no FK) |
| `created_at` | INTEGER | ms |
| `updated_at` | INTEGER | ms |

Indexes:

- `idx_users_discoverable(discoverable, username)` for the directory.
- `idx_users_username(username COLLATE NOCASE)` for lookup.

There is no email, phone, password hash or real-world identifier.

### `devices`

| Column | Type | Notes |
| --- | --- | --- |
| `id` | TEXT | UUID v4 primary key (same as transport device ID) |
| `user_id` | TEXT | FK → `users(id)` `ON DELETE CASCADE` |
| `name` | TEXT | Human label, default `This device` |
| `enc_public_key` | TEXT | Base64 65-byte P-256 point (ECDH) |
| `sig_public_key` | TEXT | Base64 65-byte P-256 point (ECDSA) |
| `created_at` | INTEGER | ms |
| `last_seen_at` | INTEGER | ms |
| `revoked_at` | INTEGER | NULL while active |

Index: `idx_devices_user(user_id, revoked_at)`.

A user has one active device in this release. Recovery revokes all devices and
inserts a new active row with the same public keys. Revoked rows are retained so
their historical messages and delivery rows keep referential integrity.

`ON DELETE CASCADE` from `devices` to `encrypted_messages.sender_device_id`
means deleting a device row deletes its sent envelopes. Normal flows revoke
rather than delete; `RebindDevice` deletes a stale row only when the same
transport device ID was previously bound to another account.

### `conversations`

| Column | Type | Notes |
| --- | --- | --- |
| `id` | TEXT | UUID v4 primary key |
| `user_a` | TEXT | FK → `users(id)` `ON DELETE CASCADE` |
| `user_b` | TEXT | FK → `users(id)` `ON DELETE CASCADE` |
| `created_at` | INTEGER | ms |

Constraints:

- `CHECK (user_a < user_b)` — canonical ordering.
- Unique index `idx_conversations_pair(user_a, user_b)` — exactly one
  conversation per unordered pair.

### `conversation_members`

| Column | Type | Notes |
| --- | --- | --- |
| `conversation_id` | TEXT | FK → `conversations(id)` `ON DELETE CASCADE` |
| `user_id` | TEXT | FK → `users(id)` `ON DELETE CASCADE` |
| `joined_at` | INTEGER | ms |
| PK | | `(conversation_id, user_id)` |

Index: `idx_members_user(user_id)`.

Both participants are inserted when the conversation is created. Membership is
checked on every message, attachment and typing operation.

### `encrypted_messages`

The store-and-forward mailbox. Every content field is client-produced.

| Column | Type | Notes |
| --- | --- | --- |
| `id` | TEXT | UUID v4 primary key (client-generated) |
| `conversation_id` | TEXT | FK → `conversations(id)` `ON DELETE CASCADE` |
| `sender_user_id` | TEXT | FK → `users(id)` `ON DELETE CASCADE` |
| `sender_device_id` | TEXT | FK → `devices(id)` `ON DELETE CASCADE` |
| `recipient_user_id` | TEXT | FK → `users(id)` `ON DELETE CASCADE` |
| `envelope_type` | TEXT | `text` or `attachment` |
| `ephemeral_public_key` | TEXT | Base64 P-256 point |
| `iv` | TEXT | Base64 12 bytes |
| `ciphertext` | TEXT | Base64 AES-256-GCM output (≤ 64 KiB decoded) |
| `signature` | TEXT | Base64 64-byte ECDSA `r\|\|s` |
| `sender_signing_key` | TEXT | Base64 P-256 point, must match device |
| `client_created_at` | INTEGER | Sender clock, ms |
| `server_created_at` | INTEGER | Server clock, ms |
| `expires_at` | INTEGER | ms; retention deadline |

Indexes:

- `idx_messages_conversation(conversation_id, server_created_at, id)`
- `idx_messages_recipient(recipient_user_id, expires_at)`

Rows are deleted when all recipient devices acknowledge them, when the sender
deletes an undelivered envelope, or when retention expires. The server cannot
read `ciphertext`.

### `message_delivery`

| Column | Type | Notes |
| --- | --- | --- |
| `message_id` | TEXT | FK → `encrypted_messages(id)` `ON DELETE CASCADE` |
| `recipient_device_id` | TEXT | FK → `devices(id)` `ON DELETE CASCADE` |
| `state` | TEXT | `queued`, `delivered`, `acked` |
| `delivered_at` | INTEGER | ms, nullable |
| `acked_at` | INTEGER | ms, nullable |
| PK | | `(message_id, recipient_device_id)` |

Index: `idx_delivery_device(recipient_device_id, state)`.

State machine:

```
queued ──handed to device──▶ delivered ──client ack──▶ acked
```

The envelope row is deleted once no delivery row for it has a state other than
`acked`.

### `encrypted_attachments`

Opaque blob metadata. No filename, MIME type or plaintext size.

| Column | Type | Notes |
| --- | --- | --- |
| `id` | TEXT | UUID v4 primary key |
| `conversation_id` | TEXT | FK → `conversations(id)` `ON DELETE CASCADE` |
| `uploader_user_id` | TEXT | FK → `users(id)` `ON DELETE CASCADE` |
| `ciphertext_size` | INTEGER | Bytes on disk |
| `created_at` | INTEGER | ms |
| `expires_at` | INTEGER | ms |

Index: `idx_attachments_expiry(expires_at)`.

The blob lives at `data/attachments/<id>.bin`. Expiry removes both the row and
the file.

### `identity_backups`

| Column | Type | Notes |
| --- | --- | --- |
| `user_id` | TEXT | PK, FK → `users(id)` `ON DELETE CASCADE` |
| `ciphertext` | TEXT | Base64 AES-256-GCM backup (≤ 32 KiB decoded) |
| `iv` | TEXT | Base64 12 bytes |
| `kdf_salt` | TEXT | Base64 16 bytes |
| `kdf_iterations` | INTEGER | 100,000–2,000,000 |
| `created_at` | INTEGER | ms |
| `updated_at` | INTEGER | ms |

One backup per user. The server cannot decrypt it; the phrase never leaves the
browser. Registration requires a backup.

### `recovery_challenges`

| Column | Type | Notes |
| --- | --- | --- |
| `nonce` | TEXT | PK, 32 random bytes as hex |
| `user_id` | TEXT | FK → `users(id)` `ON DELETE CASCADE` |
| `expires_at` | INTEGER | ms, five-minute TTL |
| `used_at` | INTEGER | NULL until consumed |

Index: `idx_recovery_expiry(expires_at)`.

`ConsumeRecoveryChallenge` selects an unused, unexpired nonce inside a
transaction and marks it used, so a captured challenge cannot be replayed. The
challenge is consumed before signature verification; a failed attempt burns it.

### `app_secrets`

| Column | Type | Notes |
| --- | --- | --- |
| `name` | TEXT | Primary key |
| `value` | BLOB | Secret bytes |
| `created_at` | INTEGER | ms |

Currently holds `identity_signing_key`, a 32-byte HMAC secret used to sign device
tokens. It is created on first run and must be preserved across deploys or all
sessions are invalidated.

### `schema_migrations`

| Column | Type | Notes |
| --- | --- | --- |
| `version` | TEXT | Primary key, e.g. `003` |
| `applied_at` | INTEGER | ms |

## Relationships

```
users 1───n devices
users 1───n conversations (as user_a or user_b)
users 1───1 identity_backups
users 1───n recovery_challenges
conversations 1───n conversation_members
conversations 1───n encrypted_messages
conversations 1───n encrypted_attachments
encrypted_messages 1───n message_delivery
devices 1───n message_delivery
devices 1───n encrypted_messages (sender_device_id)
```

All foreign keys cascade on delete except `users.primary_device_id`, which is a
plain text pointer updated by the application.

## Retention and deletion

The hourly janitor in `cmd/server/main.go`:

| Data | Deleted when |
| --- | --- |
| `encrypted_messages` | `expires_at <= now`, or all recipient devices `acked` |
| `message_delivery` | cascades with its message or device |
| `encrypted_attachments` | `expires_at <= now`; blob file removed too |
| `recovery_challenges` | `expires_at <= now` |

Default lifetimes: messages 30 days, attachments 7 days, challenges 5 minutes.

## Backup and restore

- Use `sqlite3 data/vartalap.db ".backup backup.db"` or copy the database
  together with `-wal` and `-shm` while stopped.
- Copy `data/attachments/` and `data/avatars/` with the database. Attachment
  rows and files must stay consistent.
- Restoring a database without the matching attachment directory leaves
  metadata pointing at missing blobs; downloads return 404.
- Restoring an older database restores the token-signing secret, so sessions
  issued after that backup become invalid.
- There is no server-side plaintext to lose: message content exists only on
  client devices.

## Useful queries

```sql
-- active device for a username
SELECT d.* FROM devices d
JOIN users u ON u.id = d.user_id
WHERE u.username = 'alice' AND d.revoked_at IS NULL;

-- queued envelopes for a device
SELECT m.id, m.envelope_type, m.server_created_at
FROM encrypted_messages m
JOIN message_delivery d ON d.message_id = m.id
WHERE d.recipient_device_id = ? AND d.state = 'queued'
ORDER BY m.server_created_at;

-- conversations for a user
SELECT c.* FROM conversations c
JOIN conversation_members m ON m.conversation_id = c.id
WHERE m.user_id = ?;

-- expired blobs to remove manually
SELECT id FROM encrypted_attachments WHERE expires_at <= strftime('%s','now') * 1000;
```

## Schema change checklist

1. Add `migrations/00N_name.sql` (next version, never edit an old one).
2. Update repository code and tests.
3. Update this document.
4. Consider retention, cascades and index impact.
5. Run `go test ./...` and test an upgrade from an existing database file.
