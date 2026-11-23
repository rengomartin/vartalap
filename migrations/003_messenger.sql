-- Vartalap 2.0: private, end-to-end encrypted messaging.
--
-- The old public-chat tables (anonymous_users, messages) are deliberately left
-- untouched. They are never read by the new application and old public messages
-- are NOT migrated into private conversations.

CREATE TABLE IF NOT EXISTS users (
    id                TEXT PRIMARY KEY,
    username          TEXT NOT NULL UNIQUE COLLATE NOCASE,
    display_name      TEXT NOT NULL,
    avatar_updated_at INTEGER NOT NULL DEFAULT 0,
    discoverable      INTEGER NOT NULL DEFAULT 1,
    primary_device_id TEXT,
    created_at        INTEGER NOT NULL,
    updated_at        INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_users_discoverable
    ON users (discoverable, username);
CREATE INDEX IF NOT EXISTS idx_users_username
    ON users (username COLLATE NOCASE);

-- A device is the unit that holds cryptographic keys. This release supports one
-- active device per user, but the schema keeps device keys separate from the
-- user so multi-device delivery can be added without a rewrite.
CREATE TABLE IF NOT EXISTS devices (
    id             TEXT PRIMARY KEY,
    user_id        TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name           TEXT NOT NULL DEFAULT 'This device',
    enc_public_key TEXT NOT NULL,
    sig_public_key TEXT NOT NULL,
    created_at     INTEGER NOT NULL,
    last_seen_at   INTEGER NOT NULL,
    revoked_at     INTEGER
);

CREATE INDEX IF NOT EXISTS idx_devices_user
    ON devices (user_id, revoked_at);

-- Exactly one conversation per unordered pair of users. user_a < user_b is
-- enforced so the unique index can never be bypassed by swapping the order.
CREATE TABLE IF NOT EXISTS conversations (
    id         TEXT PRIMARY KEY,
    user_a     TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    user_b     TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at INTEGER NOT NULL,
    CHECK (user_a < user_b)
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_conversations_pair
    ON conversations (user_a, user_b);

CREATE TABLE IF NOT EXISTS conversation_members (
    conversation_id TEXT NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
    user_id         TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    joined_at       INTEGER NOT NULL,
    PRIMARY KEY (conversation_id, user_id)
);

CREATE INDEX IF NOT EXISTS idx_members_user
    ON conversation_members (user_id);

-- Store-and-forward ciphertext. The server can route and delete these rows but
-- cannot read them: every field is produced by the client.
CREATE TABLE IF NOT EXISTS encrypted_messages (
    id                   TEXT PRIMARY KEY,
    conversation_id      TEXT NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
    sender_user_id       TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    sender_device_id     TEXT NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    recipient_user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    envelope_type        TEXT NOT NULL,
    ephemeral_public_key TEXT NOT NULL,
    iv                   TEXT NOT NULL,
    ciphertext           TEXT NOT NULL,
    signature            TEXT NOT NULL,
    sender_signing_key   TEXT NOT NULL,
    client_created_at    INTEGER NOT NULL,
    server_created_at    INTEGER NOT NULL,
    expires_at           INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_messages_conversation
    ON encrypted_messages (conversation_id, server_created_at, id);
CREATE INDEX IF NOT EXISTS idx_messages_recipient
    ON encrypted_messages (recipient_user_id, expires_at);

CREATE TABLE IF NOT EXISTS message_delivery (
    message_id          TEXT NOT NULL REFERENCES encrypted_messages(id) ON DELETE CASCADE,
    recipient_device_id TEXT NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    state               TEXT NOT NULL DEFAULT 'queued',
    delivered_at        INTEGER,
    acked_at            INTEGER,
    PRIMARY KEY (message_id, recipient_device_id)
);

CREATE INDEX IF NOT EXISTS idx_delivery_device
    ON message_delivery (recipient_device_id, state);

-- Attachment metadata is intentionally opaque: no filename, MIME type or
-- plaintext size. The decryption key travels inside the encrypted message.
CREATE TABLE IF NOT EXISTS encrypted_attachments (
    id                TEXT PRIMARY KEY,
    conversation_id   TEXT NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
    uploader_user_id  TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    ciphertext_size   INTEGER NOT NULL,
    created_at        INTEGER NOT NULL,
    expires_at        INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_attachments_expiry
    ON encrypted_attachments (expires_at);

-- Client-encrypted identity backup. The server stores ciphertext only; the
-- recovery phrase never leaves the device.
CREATE TABLE IF NOT EXISTS identity_backups (
    user_id        TEXT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    ciphertext     TEXT NOT NULL,
    iv             TEXT NOT NULL,
    kdf_salt       TEXT NOT NULL,
    kdf_iterations INTEGER NOT NULL,
    created_at     INTEGER NOT NULL,
    updated_at     INTEGER NOT NULL
);

-- Short-lived, single-use challenges for recovery ownership proof.
CREATE TABLE IF NOT EXISTS recovery_challenges (
    nonce      TEXT PRIMARY KEY,
    user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    expires_at INTEGER NOT NULL,
    used_at    INTEGER
);

CREATE INDEX IF NOT EXISTS idx_recovery_expiry
    ON recovery_challenges (expires_at);
