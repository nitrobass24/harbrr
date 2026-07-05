-- 0013_sync_profiles.sql — named, reusable sync profiles (Prowlarr AppProfile parity).
--
-- Until now every app-sync connection pushed the full category set of each in-scope
-- indexer with app-default seed/search behavior. A sync profile lets an operator narrow
-- what a connection syncs (a category subset, within the app's own content type) and
-- override the pushed minimum-seeders and RSS/automatic/interactive toggles — the
-- Prowlarr "Sync Profile" equivalent — as a named resource a connection references by id.
--
-- `categories` is a comma-separated list of Newznab category ids, deduped+sorted by the
-- service; the toggle/min-seeder values are validated in Go, not by a DB CHECK, so a new
-- rule needs no migration (the #85 lesson). No secrets live here.

CREATE TABLE sync_profiles (
    id                        INTEGER PRIMARY KEY,
    name                      TEXT NOT NULL UNIQUE,
    categories                TEXT NOT NULL DEFAULT '',
    min_seeders               INTEGER NOT NULL DEFAULT 0,
    enable_rss                INTEGER NOT NULL DEFAULT 1,
    enable_automatic_search   INTEGER NOT NULL DEFAULT 1,
    enable_interactive_search INTEGER NOT NULL DEFAULT 1,
    created_at                TEXT NOT NULL,
    updated_at                TEXT NOT NULL
);

-- A connection references at most one profile; deleting the profile nulls the reference
-- (the connection falls back to today's default behavior, never breaks). ADD COLUMN with
-- a REFERENCES clause is allowed because the default is NULL.
ALTER TABLE app_connections ADD COLUMN sync_profile_id INTEGER REFERENCES sync_profiles(id) ON DELETE SET NULL;
