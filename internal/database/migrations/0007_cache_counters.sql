-- 0007_cache_counters.sql — durable search-cache hit/miss/suppressed counters.
--
-- One row per instance holding the cumulative hit/miss/suppressed counts that the
-- registry keeps in memory (SearchCache.instCounters) for the cache stats surface.
-- Before this table those counters were process-lifetime only and reset to zero on
-- every restart; the registry now flushes the live atomics here on the cleanup tick
-- and at shutdown, and rehydrates them at boot, so the stats survive a restart.
--
-- The global hits/misses/breakerSuppressed totals are exactly the sum of these rows
-- (every global increment is paired with a per-instance one), so no global row is
-- stored. Counts are ABSOLUTE cumulative values, written with an idempotent UPSERT.
--
-- foreign_keys is ON (see internal/database/db.go), so a row cascade-deletes when its
-- instance is removed, exactly like search_cache. updated_at is TEXT RFC3339 (UTC).
CREATE TABLE cache_counters (
	instance_id INTEGER PRIMARY KEY REFERENCES indexer_instances(id) ON DELETE CASCADE,
	hits        INTEGER NOT NULL DEFAULT 0,
	misses      INTEGER NOT NULL DEFAULT 0,
	suppressed  INTEGER NOT NULL DEFAULT 0,
	updated_at  TEXT    NOT NULL
);
