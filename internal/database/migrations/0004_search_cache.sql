-- 0004_search_cache.sql — search-results cache (cache-aside + SWR).
--
-- One row per (instance, canonical query) keyed by cache_key, a SHA-256 hex of a
-- schema-versioned canonical payload (instance_id + the search.Query fields that
-- drive the tracker request). results_json holds the FULL pre-/dl-rewrite,
-- pre-dedupe/pre-filter release slice returned by the engine's Search seam, as a
-- JSON BLOB. SECRETS-AT-REST: those releases' Link/Magnet embed tracker passkeys
-- for some trackers, so results_json is stored PLAINTEXT in the 0600 DB (never
-- routed through internal/secrets) and must NEVER be logged. Store/Fetch/decode
-- errors wrap ONLY cache_key/instance_id — never the payload, link, or row body.
--
-- Timestamps are TEXT RFC3339 (UTC), the universal convention. foreign_keys is ON
-- (see internal/database/db.go), so a row cascade-deletes when its instance is
-- removed; Update/SetEnabled invalidate explicitly. hit_count is owned solely by
-- Touch (+1 per served hit) and survives a refresh write-back (Store's DO UPDATE
-- excludes it).
CREATE TABLE search_cache (
	cache_key     TEXT PRIMARY KEY,
	instance_id   INTEGER NOT NULL REFERENCES indexer_instances(id) ON DELETE CASCADE,
	results_json  BLOB NOT NULL,
	total_results INTEGER NOT NULL,
	cached_at     TEXT NOT NULL,
	last_used_at  TEXT NOT NULL,
	expires_at    TEXT NOT NULL,
	hit_count     INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX search_cache_instance_idx ON search_cache (instance_id);
CREATE INDEX search_cache_expires_idx ON search_cache (expires_at);
