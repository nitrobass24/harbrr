-- 0028_drop_dead_sync_columns.sql — drop the dead sync-model columns 0024 deliberately
-- left behind (#370).
--
-- 0024 (#365) turned a sync profile into pure indexer routing: all sync behavior moved
-- onto indexer_instances, and the per-connection selected-scope machinery
-- (app_connections.index_scope + the app_connection_indexers.selected intent flag) was
-- replaced by a routing profile's sync_profile_indexers set. 0024 transformed the data but
-- kept the old columns so a boot on the new code could not read old-shape data; #367 then
-- removed every read and write of them (database/syncprofiles.go's profileColumns is now
-- `id, name, created_at, updated_at`; appconnections.go's connectionColumns has no
-- index_scope; the ledger UPSERT/SELECT list no `selected`). No statement anywhere uses
-- `SELECT *` on these tables, so there is no positional-scan to shift either.
--
-- NOTE: min_seeders / enable_rss / enable_automatic_search / enable_interactive_search
-- also exist on indexer_instances, where 0023/0024 made them the live source. Only the
-- sync_profiles copies are dead — read the table name on each statement below.
--
-- Backup/restore is unaffected: bundle.go's IndexScope / SelectedInstanceIDs fields are
-- decode-only legacy read out of an OLD BUNDLE's JSON (never out of these columns) to mint
-- a routing profile on import.
--
-- ORDER MATTERS. The app_connections rebuild at the bottom stages and restores its FK
-- child app_connection_indexers with positional `SELECT *` (see the trap note there), so
-- the child's column shape must be identical at stage time and at restore time. Every
-- plain DROP COLUMN therefore runs FIRST — including the child's own `selected` — and the
-- rebuild LAST. Dropping a child column between the stage and the restore would be a
-- positional-insert mismatch or, worse, a silent column shift.

-- Plain drops: none of these columns participates in an index, constraint, view or
-- trigger (sync_profiles' only index is the implicit one on its UNIQUE name;
-- app_connection_indexers has UNIQUE(connection_id, instance_id) plus an index on
-- connection_id), so SQLite drops them directly — the 0022 precedent, not 0021's rebuild.
ALTER TABLE sync_profiles DROP COLUMN categories;
ALTER TABLE sync_profiles DROP COLUMN min_seeders;
ALTER TABLE sync_profiles DROP COLUMN enable_rss;
ALTER TABLE sync_profiles DROP COLUMN enable_automatic_search;
ALTER TABLE sync_profiles DROP COLUMN enable_interactive_search;

ALTER TABLE app_connection_indexers DROP COLUMN selected;

-- app_connections rebuild: index_scope carries a CHECK constraint, which SQLite will not
-- let DROP COLUMN touch, so the table is rebuilt (stage/drop/rename) exactly as 0008 and
-- 0021 did.
--
-- THE TRAP: app_connection_indexers.connection_id is REFERENCES app_connections(id) ON
-- DELETE CASCADE (0003) and PRAGMA foreign_keys is ON (internal/database/db.go), so
-- `DROP TABLE app_connections` performs an implicit DELETE FROM that cascades and wipes
-- the whole sync ledger. The ledger is staged into a backup table before the DROP and
-- restored against the rebuilt parent afterwards; row ids are preserved on both sides, so
-- the restored FK references resolve.
--
-- The surviving CHECKs (sync_level, freeleech_mode) and the sync_profile_id FK with its
-- ON DELETE SET NULL are reproduced verbatim; only index_scope and its CHECK go. Note the
-- shape below is 0021's minus BOTH index_scope and `priority` — 0023 already dropped
-- priority off this table (it moved to indexer_instances), so copying 0021's column list
-- wholesale would resurrect it.
--
-- The one index on app_connections (confirmed against a migrated DB's sqlite_master, not a
-- grep) is 0021's partial UNIQUE(app_id) WHERE app_id IS NOT NULL. Unlike 0021 it is recreated
-- AFTER the drop/rename rather than up-front on the _new table: index names are global in
-- SQLite, and 0021's index of that name is still attached to the old table until the
-- DROP TABLE removes it, so creating it early fails with "index ... already exists".
CREATE TABLE app_connections_new (
	id                       INTEGER PRIMARY KEY,
	name                     TEXT NOT NULL,
	kind                     TEXT NOT NULL,
	app_id                   INTEGER REFERENCES apps(id),
	harbrr_api_key_id        INTEGER REFERENCES api_keys(id) ON DELETE SET NULL,
	harbrr_api_key_encrypted TEXT NOT NULL,
	key_id                   TEXT NOT NULL,
	enabled                  INTEGER NOT NULL DEFAULT 1,
	sync_level               TEXT NOT NULL DEFAULT 'full' CHECK (sync_level IN ('full', 'add_update')),
	freeleech_mode           TEXT NOT NULL DEFAULT 'honor' CHECK (freeleech_mode IN ('honor', 'bypass')),
	sync_profile_id          INTEGER REFERENCES sync_profiles(id) ON DELETE SET NULL,
	last_sync_at             TEXT,
	last_sync_status         TEXT,
	last_sync_error          TEXT,
	created_at               TEXT NOT NULL,
	updated_at               TEXT NOT NULL
);

INSERT INTO app_connections_new (
	id, name, kind, app_id, harbrr_api_key_id, harbrr_api_key_encrypted, key_id,
	enabled, sync_level, freeleech_mode, sync_profile_id,
	last_sync_at, last_sync_status, last_sync_error, created_at, updated_at
)
SELECT
	id, name, kind, app_id, harbrr_api_key_id, harbrr_api_key_encrypted, key_id,
	enabled, sync_level, freeleech_mode, sync_profile_id,
	last_sync_at, last_sync_status, last_sync_error, created_at, updated_at
FROM app_connections;

CREATE TABLE app_connection_indexers_backup AS SELECT * FROM app_connection_indexers;

DROP TABLE app_connections;
ALTER TABLE app_connections_new RENAME TO app_connections;

CREATE UNIQUE INDEX app_connections_app_id_uq ON app_connections (app_id) WHERE app_id IS NOT NULL;

INSERT INTO app_connection_indexers SELECT * FROM app_connection_indexers_backup;
DROP TABLE app_connection_indexers_backup;
