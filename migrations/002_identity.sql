-- Server-held secrets. The identity signing key lives here so signed device
-- tokens stay valid across restarts without an extra environment variable.
CREATE TABLE IF NOT EXISTS app_secrets (
    name       TEXT PRIMARY KEY,
    value      BLOB NOT NULL,
    created_at INTEGER NOT NULL
);
