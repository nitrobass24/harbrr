-- 0023_indexer_sync_fields.sql — per-indexer priority + minimum seeders (#364).
--
-- Priority (1-50, 1 = highest, Prowlarr semantics) moves from a single value stamped
-- onto every indexer a connection pushes (app_connections.priority) to a first-class,
-- list-visible column on indexer_instances: each indexer now carries its own priority,
-- consumed by the appsync desired-state builder (internal/appsync/sync.go) instead of
-- the connection's. min_seeders is new: a per-indexer floor that overrides the sync
-- profile's Minimum Seeders when set (0 = unset, falls back to the profile value),
-- pushed as minimumSeeders (torrent-only, mirroring Prowlarr's "Apps Minimum Seeders").
--
-- app_connections.priority becomes redundant once every indexer carries its own value
-- and is dropped here. It has no CHECK, index, or FK referencing it (see 0021's
-- rebuild of this table), so — like 0022's proxies.url_encrypted — this is a plain
-- DROP COLUMN, not a stage/drop/rename rebuild.
ALTER TABLE indexer_instances ADD COLUMN priority INTEGER NOT NULL DEFAULT 25;
ALTER TABLE indexer_instances ADD COLUMN min_seeders INTEGER NOT NULL DEFAULT 0;

ALTER TABLE app_connections DROP COLUMN priority;
