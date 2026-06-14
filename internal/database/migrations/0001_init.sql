-- 0001_init.sql — Phase 4 daemon-foundation schema.
--
-- Forward-only. Timestamps are TEXT RFC3339 (UTC). All placeholders in repository
-- SQL are `?` (SQLite-native). Secrets are NEVER stored in recoverable form except
-- tracker credentials, which live AES-256-GCM-encrypted in *_encrypted columns
-- (see internal/secrets, docs/ideas.md §9).

-- The single admin (first-run setup enforces one). The password is an argon2id
-- PHC hash — one-way, never recoverable, so a key compromise never yields it.
CREATE TABLE users (
	id            INTEGER PRIMARY KEY,
	username      TEXT NOT NULL UNIQUE,
	password_hash TEXT NOT NULL,
	created_at    TEXT NOT NULL,
	updated_at    TEXT NOT NULL
);

-- Management API keys and the *arr-facing Torznab apikey. Stored as a SHA-256
-- hash; the plaintext is shown to the user exactly once at mint time.
CREATE TABLE api_keys (
	id           INTEGER PRIMARY KEY,
	name         TEXT NOT NULL,
	key_hash     TEXT NOT NULL UNIQUE,
	created_at   TEXT NOT NULL,
	last_used_at TEXT
);

-- A configured indexer instance. `slug` is the stable, user-facing identifier
-- used as the Torznab {indexerId} path segment and the management resource id;
-- the integer `id` is internal and stable, so it backs the encryption AAD.
CREATE TABLE indexer_instances (
	id            INTEGER PRIMARY KEY,
	slug          TEXT NOT NULL UNIQUE,
	definition_id TEXT NOT NULL,
	name          TEXT NOT NULL,
	base_url      TEXT,
	enabled       INTEGER NOT NULL DEFAULT 1,
	created_at    TEXT NOT NULL,
	updated_at    TEXT NOT NULL
);

-- One row per configured setting of an instance. A secret setting stores its
-- value in value_encrypted (base64 nonce‖ciphertext‖tag) with the key_id that
-- encrypted it and is_secret=1; a plaintext setting stores value and is_secret=0.
CREATE TABLE indexer_settings (
	id              INTEGER PRIMARY KEY,
	instance_id     INTEGER NOT NULL REFERENCES indexer_instances(id) ON DELETE CASCADE,
	name            TEXT NOT NULL,
	value           TEXT,
	value_encrypted TEXT,
	key_id          TEXT,
	is_secret       INTEGER NOT NULL DEFAULT 0,
	UNIQUE (instance_id, name)
);

CREATE INDEX indexer_settings_instance_idx ON indexer_settings (instance_id);

-- Small key/value table for application metadata. Holds the secrets key_id and an
-- encrypted canary used to fail loud at startup on a wrong/changed encryption key.
CREATE TABLE app_meta (
	key   TEXT PRIMARY KEY,
	value TEXT NOT NULL
);

-- Server-side session store backing alexedwards/scs/v2 (custom driver-agnostic
-- store). expiry is Unix seconds (REAL) — our own convention, not scs's schema.
CREATE TABLE sessions (
	token  TEXT PRIMARY KEY,
	data   BLOB NOT NULL,
	expiry REAL NOT NULL
);

CREATE INDEX sessions_expiry_idx ON sessions (expiry);
