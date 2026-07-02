-- 0003_appsync.sql — Phase 10 *arr/qui application sync.
--
-- Two tables: the configured target apps (app_connections) and the per-(connection,
-- indexer) reconciliation ledger (app_connection_indexers). Timestamps are TEXT
-- RFC3339 (UTC), the universal convention. Secrets (api_key_encrypted,
-- harbrr_api_key_encrypted) are base64 nonce‖ciphertext‖tag, encrypted by the
-- service under key_id with the connection id as AAD — exactly like indexer
-- settings; cleartext never reaches these columns. Sync errors (last_sync_error,
-- last_push_error) are RedactError-scrubbed before storage. foreign_keys is ON (see
-- internal/database/db.go), so ledger rows cascade-delete with their parent.
CREATE TABLE app_connections (
	id                       INTEGER PRIMARY KEY,
	name                     TEXT NOT NULL,
	kind                     TEXT NOT NULL CHECK (kind IN ('sonarr', 'radarr', 'qui')),
	-- base_url is the app's own API base (e.g. http://sonarr:8989).
	base_url                 TEXT NOT NULL,
	-- the app's API key, so harbrr can call it (secret).
	api_key_encrypted        TEXT NOT NULL,
	-- the base URL THIS app uses to reach harbrr's Torznab feed (may differ per app).
	harbrr_url               TEXT NOT NULL,
	-- the dedicated harbrr API key minted for this connection: id for revocation,
	-- plaintext (secret) persisted so every re-sync can re-push it into the app.
	-- SET NULL if the key is revoked out of band (the connection then needs a resync).
	harbrr_api_key_id        INTEGER REFERENCES api_keys(id) ON DELETE SET NULL,
	harbrr_api_key_encrypted TEXT NOT NULL,
	-- key_id encrypts both secret columns above.
	key_id                   TEXT NOT NULL,
	enabled                  INTEGER NOT NULL DEFAULT 1,
	sync_level               TEXT NOT NULL DEFAULT 'full' CHECK (sync_level IN ('full', 'add_update')),
	index_scope              TEXT NOT NULL DEFAULT 'all' CHECK (index_scope IN ('all', 'selected')),
	priority                 INTEGER NOT NULL DEFAULT 25,
	last_sync_at             TEXT,
	last_sync_status         TEXT,
	last_sync_error          TEXT,
	created_at               TEXT NOT NULL,
	updated_at               TEXT NOT NULL,
	UNIQUE (kind, base_url)
);

-- Per-(connection, harbrr instance) sync ledger: the authoritative reconciliation
-- state. remote_id is the id the target app assigned the pushed indexer (NULL until
-- first push). payload_hash short-circuits unchanged updates. UNIQUE(connection_id,
-- instance_id) backs the upsert seam.
CREATE TABLE app_connection_indexers (
	id               INTEGER PRIMARY KEY,
	connection_id    INTEGER NOT NULL REFERENCES app_connections(id) ON DELETE CASCADE,
	instance_id      INTEGER NOT NULL REFERENCES indexer_instances(id) ON DELETE CASCADE,
	remote_id        TEXT,
	selected         INTEGER NOT NULL DEFAULT 1,
	payload_hash     TEXT,
	last_pushed_at   TEXT,
	last_push_status TEXT,
	last_push_error  TEXT,
	UNIQUE (connection_id, instance_id)
);

CREATE INDEX app_connection_indexers_conn_idx ON app_connection_indexers (connection_id);
