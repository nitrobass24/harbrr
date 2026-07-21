-- 0021_drop_legacy_app_columns.sql — drop the legacy per-row identity/credential columns
-- now that every surface row's identity lives on a first-class App (ADR 0004, #269).
--
-- 0020 added app_id to app_connections/announce_connections/download_clients but kept the
-- legacy base_url/api_key_encrypted/harbrr_url (host/username/secret_encrypted for download
-- clients) columns for the boot fold's window (internal/resourcemigrate.FoldApps, removed in
-- this same PR now that this migration makes the fold a permanent no-op — see the guard
-- below). Those columns are dropped here. SQLite cannot DROP COLUMN a column that
-- participates in an index/constraint (the old UNIQUE(kind, base_url)) and this migration
-- also moves that uniqueness onto app_id, so each table is rebuilt (stage/drop/rename), the
-- same 0008 precedent used for app_connections' freeleech_mode add + CHECK drop. The two
-- push tables (app_connections, announce_connections) get a partial `UNIQUE(app_id) WHERE
-- app_id IS NOT NULL` in place of the old `UNIQUE(kind, base_url)`; download_clients gets NO
-- app_id uniqueness (two clients may legitimately share one App, e.g. two qBittorrent
-- profiles against the same instance).
--
-- This drop is also the purge of any stale legacy ciphertext: a fold never cleared the row's
-- own api_key_encrypted/secret_encrypted after copying the credential onto the App, and an
-- App credential rotated post-fold (PATCH /api/apps/{id}) never touched the row's copy either
-- — so a legacy column could already be holding a stale, wrong ciphertext before this
-- migration deletes it. Nothing reads it as authoritative today (see the service doc
-- comments this PR updates), so the loss is a bugfix, not a behavior change.
--
-- Guard: refuse to apply while any non-hostless row still has a NULL app_id (an un-folded
-- row). The boot order (internal/app/app.go: db.Migrate fully succeeds before FoldApps ever
-- runs, and a Migrate error is fatal) means this migration always runs either against a
-- fresh DB or against a DB where a prior boot's fold has already completed — so by the time
-- this guard passes, dropping the columns is permanently safe and FoldApps becomes dead code
-- (removed in this PR). A host-less download client (blackhole) has no identity to fold and
-- is exempted (host = '' by construction — see internal/download's hostless kinds).
CREATE TEMP TABLE _0021_guard (x INTEGER);

CREATE TEMP TRIGGER _0021_guard_check
AFTER INSERT ON _0021_guard
BEGIN
	SELECT RAISE(ABORT, 'harbrr: cannot apply migration 0021 - one or more app-sync/announce/download-client connections have not yet been folded into the App registry; run the release immediately before this one once more (it retries the fold on every boot and logs the outcome), confirm no more "app migration pending" warnings appear, then upgrade to this version')
	WHERE
		(SELECT COUNT(*) FROM app_connections WHERE app_id IS NULL) > 0
		OR (SELECT COUNT(*) FROM announce_connections WHERE app_id IS NULL) > 0
		OR (SELECT COUNT(*) FROM download_clients WHERE app_id IS NULL AND host != '') > 0;
END;

INSERT INTO _0021_guard (x) VALUES (1);

DROP TRIGGER _0021_guard_check;
DROP TABLE _0021_guard;

-- app_connections rebuild (FK-child-aware: app_connection_indexers is staged into a backup
-- table before the DROP, which would otherwise cascade and wipe it, then restored against
-- the rebuilt parent — ids are preserved so the restored FK references resolve).
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
	index_scope              TEXT NOT NULL DEFAULT 'all' CHECK (index_scope IN ('all', 'selected')),
	freeleech_mode           TEXT NOT NULL DEFAULT 'honor' CHECK (freeleech_mode IN ('honor', 'bypass')),
	priority                 INTEGER NOT NULL DEFAULT 25,
	sync_profile_id          INTEGER REFERENCES sync_profiles(id) ON DELETE SET NULL,
	last_sync_at             TEXT,
	last_sync_status         TEXT,
	last_sync_error          TEXT,
	created_at               TEXT NOT NULL,
	updated_at               TEXT NOT NULL
);

-- Partial unique index: SQLite cannot express a partial constraint inline as a table-level
-- UNIQUE, so it is a separate index (as opposed to 0008/0003's inline UNIQUE(kind, base_url),
-- which had no NULL column to exempt).
CREATE UNIQUE INDEX app_connections_app_id_uq ON app_connections_new (app_id) WHERE app_id IS NOT NULL;

INSERT INTO app_connections_new (
	id, name, kind, app_id, harbrr_api_key_id, harbrr_api_key_encrypted, key_id,
	enabled, sync_level, index_scope, freeleech_mode, priority, sync_profile_id,
	last_sync_at, last_sync_status, last_sync_error, created_at, updated_at
)
SELECT
	id, name, kind, app_id, harbrr_api_key_id, harbrr_api_key_encrypted, key_id,
	enabled, sync_level, index_scope, freeleech_mode, priority, sync_profile_id,
	last_sync_at, last_sync_status, last_sync_error, created_at, updated_at
FROM app_connections;

CREATE TABLE app_connection_indexers_backup AS SELECT * FROM app_connection_indexers;

DROP TABLE app_connections;
ALTER TABLE app_connections_new RENAME TO app_connections;

INSERT INTO app_connection_indexers SELECT * FROM app_connection_indexers_backup;
DROP TABLE app_connection_indexers_backup;

-- announce_connections rebuild: same shape, no FK child to stage/restore.
CREATE TABLE announce_connections_new (
	id                       INTEGER PRIMARY KEY,
	name                     TEXT NOT NULL,
	kind                     TEXT NOT NULL,
	app_id                   INTEGER REFERENCES apps(id),
	harbrr_api_key_id        INTEGER REFERENCES api_keys(id) ON DELETE SET NULL,
	harbrr_api_key_encrypted TEXT NOT NULL,
	key_id                   TEXT NOT NULL,
	enabled                  INTEGER NOT NULL DEFAULT 1,
	created_at               TEXT NOT NULL,
	updated_at               TEXT NOT NULL
);

CREATE UNIQUE INDEX announce_connections_app_id_uq ON announce_connections_new (app_id) WHERE app_id IS NOT NULL;

INSERT INTO announce_connections_new (
	id, name, kind, app_id, harbrr_api_key_id, harbrr_api_key_encrypted, key_id, enabled, created_at, updated_at
)
SELECT
	id, name, kind, app_id, harbrr_api_key_id, harbrr_api_key_encrypted, key_id, enabled, created_at, updated_at
FROM announce_connections;

DROP TABLE announce_connections;
ALTER TABLE announce_connections_new RENAME TO announce_connections;

-- download_clients rebuild: same shape, no FK child. UNIQUE(name) is kept exactly as-is;
-- app_id deliberately gets NO uniqueness (two clients may share one App).
CREATE TABLE download_clients_new (
	id            INTEGER PRIMARY KEY,
	name          TEXT NOT NULL,
	kind          TEXT NOT NULL,
	app_id        INTEGER REFERENCES apps(id),
	enabled       INTEGER NOT NULL DEFAULT 1,
	key_id        TEXT NOT NULL,
	settings_json TEXT NOT NULL DEFAULT '{}',
	created_at    TEXT NOT NULL,
	updated_at    TEXT NOT NULL,
	UNIQUE (name)
);

INSERT INTO download_clients_new (id, name, kind, app_id, enabled, key_id, settings_json, created_at, updated_at)
SELECT id, name, kind, app_id, enabled, key_id, settings_json, created_at, updated_at
FROM download_clients;

DROP TABLE download_clients;
ALTER TABLE download_clients_new RENAME TO download_clients;
