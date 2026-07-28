-- 0024_sync_profile_routing.sql — sync profiles become pure indexer routing sets (#365).
--
-- Until now a sync profile mixed routing (which indexers) and behavior (categories,
-- min seeders, search toggles); which indexers a connection synced lived separately, per
-- connection (index_scope + the app_connection_indexers.selected ledger flag). That meant
-- behavior could never differ per indexer (#73) and a selection was not reusable across
-- apps. From here: a profile is name + a selected set of indexer instances (the new
-- sync_profile_indexers join table); no profile, or a profile with an empty selection,
-- means every compatible indexer (mirrors the empty-categories convention, and avoids a
-- profile edit silently narrowing to nothing). All sync behavior — categories, RSS/
-- automatic/interactive-search toggles — moves onto indexer_instances, alongside the
-- min_seeders column #364 already added there (which becomes the sole source; the
-- profile-level fallback goes away).
--
-- This migration only ADDS schema and transforms data in place. It does NOT drop
-- app_connections.index_scope (its CHECK constraint means dropping it needs a full table
-- rebuild, like #269/0021) or sync_profiles' now-dead behavioral columns — both are left
-- for a later cleanup migration once the code is proven to no longer read them. Running
-- the transform inside this migration (rather than as a one-time boot-time Go pass) means
-- a failure aborts boot outright — there is no window where new code reads old-shape data.
ALTER TABLE indexer_instances ADD COLUMN enable_rss INTEGER NOT NULL DEFAULT 1;
ALTER TABLE indexer_instances ADD COLUMN enable_automatic_search INTEGER NOT NULL DEFAULT 1;
ALTER TABLE indexer_instances ADD COLUMN enable_interactive_search INTEGER NOT NULL DEFAULT 1;
ALTER TABLE indexer_instances ADD COLUMN sync_categories TEXT NOT NULL DEFAULT '';

CREATE TABLE sync_profile_indexers (
    profile_id  INTEGER NOT NULL REFERENCES sync_profiles(id) ON DELETE CASCADE,
    instance_id INTEGER NOT NULL REFERENCES indexer_instances(id) ON DELETE CASCADE,
    PRIMARY KEY (profile_id, instance_id)
);

-- a. Behavioral backfill: only when every connection referencing a profile references the
-- SAME one profile is the mapping unambiguous, so every instance inherits its behavior
-- (min_seeders only when the instance has no override of its own — #364's per-indexer
-- floor wins). Zero or multiple distinct referenced profiles leaves the defaults (toggles
-- on, all categories) — there is no correct per-indexer split of an ambiguous mapping.
-- Runs BEFORE (b)/(c) so it reads the pre-migration connection->profile references.
UPDATE indexer_instances SET
  enable_rss                = (SELECT enable_rss FROM sync_profiles WHERE id = (SELECT DISTINCT sync_profile_id FROM app_connections WHERE sync_profile_id IS NOT NULL)),
  enable_automatic_search   = (SELECT enable_automatic_search   FROM sync_profiles WHERE id = (SELECT DISTINCT sync_profile_id FROM app_connections WHERE sync_profile_id IS NOT NULL)),
  enable_interactive_search = (SELECT enable_interactive_search FROM sync_profiles WHERE id = (SELECT DISTINCT sync_profile_id FROM app_connections WHERE sync_profile_id IS NOT NULL)),
  sync_categories           = (SELECT categories FROM sync_profiles WHERE id = (SELECT DISTINCT sync_profile_id FROM app_connections WHERE sync_profile_id IS NOT NULL)),
  min_seeders = CASE WHEN min_seeders = 0 THEN (SELECT min_seeders FROM sync_profiles WHERE id = (SELECT DISTINCT sync_profile_id FROM app_connections WHERE sync_profile_id IS NOT NULL)) ELSE min_seeders END
WHERE (SELECT COUNT(DISTINCT sync_profile_id) FROM app_connections WHERE sync_profile_id IS NOT NULL) = 1;

-- b. Mint a routing profile for every index_scope='selected' connection from its ledger
-- selection (id-suffixed name so it can never collide with an existing sync_profiles.name).
-- A selected-scope connection that also carried a behavioral profile gets its reference
-- replaced by the minted routing profile here — its behavior was already folded onto the
-- instances by (a), or defaulted, so nothing is lost.
INSERT INTO sync_profiles (name, created_at, updated_at)
SELECT c.name || ' indexers (' || c.id || ')', c.created_at, c.updated_at
FROM app_connections c WHERE c.index_scope = 'selected';

INSERT INTO sync_profile_indexers (profile_id, instance_id)
SELECT p.id, l.instance_id
FROM app_connections c
JOIN sync_profiles p ON p.name = c.name || ' indexers (' || c.id || ')'
JOIN app_connection_indexers l ON l.connection_id = c.id AND l.selected = 1
WHERE c.index_scope = 'selected';

UPDATE app_connections SET sync_profile_id =
  (SELECT p.id FROM sync_profiles p WHERE p.name = app_connections.name || ' indexers (' || app_connections.id || ')')
WHERE index_scope = 'selected';

-- c. Defensive neutralization: the column stays (see header comment) but code stops
-- reading it from here on, so no connection is left claiming a scope the engine ignores.
UPDATE app_connections SET index_scope = 'all';
