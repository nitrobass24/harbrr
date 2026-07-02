-- 0009_announce_connections.sql — Phase 11 cross-seed announce targets.
--
-- A configured cross-seed tool harbrr PUSHES new releases to (qui cross-seed, cross-seed
-- v6) — the announce-source/messenger half of the feature. Unlike app_connections (which
-- mirrors harbrr's indexer *feed* and keeps a per-indexer reconciliation ledger), an
-- announce target is fire-and-forget: harbrr offers a release, the tool decides. So there
-- is no ledger, no sync level, no index scope — just the connection + its two secrets.
--
-- Secrets are base64 nonce‖ciphertext‖tag, encrypted under key_id with the connection id
-- as AAD, exactly like app_connections: api_key_encrypted is the *tool's* API key (so
-- harbrr can call it), harbrr_api_key_encrypted is the dedicated minted harbrr key whose
-- plaintext signs the /dl link cross-seed/qui fetch back. foreign_keys is ON.
CREATE TABLE announce_connections (
	id                       INTEGER PRIMARY KEY,
	name                     TEXT NOT NULL,
	-- kind is validated in Go (internal/announce + service); no DB CHECK, so a new tool
	-- kind needs no migration (the lesson of #85).
	kind                     TEXT NOT NULL,
	base_url                 TEXT NOT NULL,
	api_key_encrypted        TEXT NOT NULL,
	-- the base URL the tool uses to reach harbrr's /dl link. Both kinds need it (qui
	-- fetches the link server-side, cross-seed v6 fetches it itself); the service requires
	-- it on create. DEFAULT '' only covers the create-then-set-secrets insert window.
	harbrr_url               TEXT NOT NULL DEFAULT '',
	harbrr_api_key_id        INTEGER REFERENCES api_keys(id) ON DELETE SET NULL,
	harbrr_api_key_encrypted TEXT NOT NULL,
	key_id                   TEXT NOT NULL,
	enabled                  INTEGER NOT NULL DEFAULT 1,
	created_at               TEXT NOT NULL,
	updated_at               TEXT NOT NULL,
	UNIQUE (kind, base_url)
);
