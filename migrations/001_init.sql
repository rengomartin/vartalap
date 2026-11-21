-- Vartalap initial schema. Embedded by migrations/migrations.go.
CREATE TABLE IF NOT EXISTS anonymous_users (
    device_id    TEXT PRIMARY KEY,
    display_name TEXT NOT NULL,
    created_at   INTEGER NOT NULL,
    updated_at   INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS messages (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    device_id   TEXT NOT NULL,
    sender_name TEXT NOT NULL,
    content     TEXT NOT NULL,
    created_at  INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_messages_id_desc ON messages (id DESC);
CREATE INDEX IF NOT EXISTS idx_messages_created_at_desc ON messages (created_at DESC, id DESC);
