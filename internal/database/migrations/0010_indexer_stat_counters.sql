-- 0010_indexer_stat_counters.sql — durable per-indexer query/grab/latency counters.
--
-- One row per instance holding the cumulative query count, grab count, and total
-- response time the registry keeps in memory (IndexerStats.inst) for the per-indexer
-- stats surface. Before this table those counters were process-lifetime only and reset
-- to zero on every restart; the registry now flushes the live atomics here on a periodic
-- tick and at shutdown, and rehydrates them at boot, so the stats survive a restart.
--
-- Failure counts and the last-failure timestamp are NOT stored here — they are folded in
-- at read time from indexer_health_events (the existing append-only table), so no failure
-- data is duplicated. avgResponseMs is derived (response_ms_total / queries), not stored,
-- keeping the write an idempotent absolute UPSERT.
--
-- foreign_keys is ON (see internal/database/db.go), so a row cascade-deletes when its
-- instance is removed, exactly like cache_counters. Timestamps are TEXT RFC3339 (UTC);
-- last_query_at/last_grab_at are nullable (an indexer may have been grabbed but never
-- searched, or vice-versa). Counts are ABSOLUTE cumulative values.
CREATE TABLE indexer_stat_counters (
	instance_id       INTEGER PRIMARY KEY REFERENCES indexer_instances(id) ON DELETE CASCADE,
	queries           INTEGER NOT NULL DEFAULT 0,
	grabs             INTEGER NOT NULL DEFAULT 0,
	response_ms_total INTEGER NOT NULL DEFAULT 0,
	last_query_at     TEXT,
	last_grab_at      TEXT,
	updated_at        TEXT    NOT NULL
);
