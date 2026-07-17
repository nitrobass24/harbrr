CREATE TABLE download_clients (
    id               INTEGER PRIMARY KEY,
    name             TEXT NOT NULL,
    kind             TEXT NOT NULL,           -- validated in Go, no CHECK (#85 lesson)
    enabled          INTEGER NOT NULL DEFAULT 1,
    host             TEXT NOT NULL,
    username         TEXT NOT NULL DEFAULT '',
    secret_encrypted TEXT NOT NULL,
    key_id           TEXT NOT NULL,
    settings_json    TEXT NOT NULL DEFAULT '{}',
    created_at       TEXT NOT NULL,
    updated_at       TEXT NOT NULL,
    UNIQUE (name)
);
