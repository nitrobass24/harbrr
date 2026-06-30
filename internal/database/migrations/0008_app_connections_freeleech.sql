-- 0008_app_connections_freeleech.sql — Phase 11 per-app freeleech routing + #85 fix.
--
-- Two changes to app_connections, both requiring a table rebuild (SQLite cannot drop
-- or alter a CHECK constraint in place):
--
--   1. Add freeleech_mode ('honor'|'bypass') — the per-connection routing knob. app-sync
--      pushes the honor feed URL to *arrs (which honor the indexer's freeleech setting)
--      and the /full bypass URL to qui/cross-seed (which need the full catalog).
--
--   2. Drop the kind CHECK. The 0003 CHECK only allowed ('sonarr','radarr','qui') and was
--      never widened when lidarr/readarr/whisparr shipped (#78), so creating those three
--      connections failed at INSERT even though Go's validateKind accepts them (#85). That
--      drift between two sources of truth IS the bug; we delete the duplicate and let
--      internal/appsync/validate.go validateKind be the single source of truth. New app
--      kinds (e.g. Mylar) then need no migration. The stable closed enums (sync_level,
--      index_scope, and the new freeleech_mode) keep their CHECKs.
--
-- foreign_keys is ON (db.go) and migrations run in one transaction (migrate.go), so the
-- pragma cannot be toggled here. A bare DROP of app_connections would fire the
-- app_connection_indexers ON DELETE CASCADE and wipe the ledger, so the ledger is staged
-- into a temp table and restored against the rebuilt parent (row ids are preserved, so the
-- restored FK references resolve).

CREATE TABLE app_connections_new (
	id                       INTEGER PRIMARY KEY,
	name                     TEXT NOT NULL,
	kind                     TEXT NOT NULL,
	base_url                 TEXT NOT NULL,
	api_key_encrypted        TEXT NOT NULL,
	harbrr_url               TEXT NOT NULL,
	harbrr_api_key_id        INTEGER REFERENCES api_keys(id) ON DELETE SET NULL,
	harbrr_api_key_encrypted TEXT NOT NULL,
	key_id                   TEXT NOT NULL,
	enabled                  INTEGER NOT NULL DEFAULT 1,
	sync_level               TEXT NOT NULL DEFAULT 'full' CHECK (sync_level IN ('full', 'add_update')),
	index_scope              TEXT NOT NULL DEFAULT 'all' CHECK (index_scope IN ('all', 'selected')),
	freeleech_mode           TEXT NOT NULL DEFAULT 'honor' CHECK (freeleech_mode IN ('honor', 'bypass')),
	priority                 INTEGER NOT NULL DEFAULT 25,
	last_sync_at             TEXT,
	last_sync_status         TEXT,
	last_sync_error          TEXT,
	created_at               TEXT NOT NULL,
	updated_at               TEXT NOT NULL,
	UNIQUE (kind, base_url)
);

INSERT INTO app_connections_new (
	id, name, kind, base_url, api_key_encrypted, harbrr_url, harbrr_api_key_id,
	harbrr_api_key_encrypted, key_id, enabled, sync_level, index_scope, freeleech_mode,
	priority, last_sync_at, last_sync_status, last_sync_error, created_at, updated_at
)
SELECT
	id, name, kind, base_url, api_key_encrypted, harbrr_url, harbrr_api_key_id,
	harbrr_api_key_encrypted, key_id, enabled, sync_level, index_scope, 'honor',
	priority, last_sync_at, last_sync_status, last_sync_error, created_at, updated_at
FROM app_connections;

-- Stage the ledger before dropping its parent (the DROP cascades and empties it).
CREATE TABLE app_connection_indexers_backup AS SELECT * FROM app_connection_indexers;

DROP TABLE app_connections;
ALTER TABLE app_connections_new RENAME TO app_connections;

-- Restore the ledger against the rebuilt parent (ids preserved, so FK references resolve).
INSERT INTO app_connection_indexers SELECT * FROM app_connection_indexers_backup;
DROP TABLE app_connection_indexers_backup;
