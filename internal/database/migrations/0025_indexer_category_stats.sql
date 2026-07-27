-- 0025_indexer_category_stats.sql — grab attempts + per-category indexer tallies.
--
-- Two additions for autobrr/harbrr#403:
--
-- 1. grab_attempts on the existing per-instance counter row. `grabs` counts only
--    SUCCESSFUL grabs; pairing it with the attempt count is what makes the success rate
--    ("grabbed 200, 40 failed") derivable. The rate itself is NEVER stored — it is
--    grabs/grab_attempts at read time.
--
-- 2. indexer_category_stats: how many results an indexer returned, and how many of its
--    releases were grabbed, per PARENT category per month. The category is folded to the
--    standard family root (2040 Movies/HD -> 2000 Movies; 0 = no mappable standard
--    category), so the row count stays ~10 per instance per month rather than one per
--    tracker category. The month bucket is deliberately coarse: this answers "is this
--    indexer earning its place for music", a renewal decision, not a graph.
--
-- Aggregation is incremental by construction (the OOM constraint in #403): the registry
-- accumulates deltas in memory and folds them into its existing flush tick with an
-- additive UPSERT, the read path is ONE grouped SQL query, and retention is a range
-- delete of old buckets. No history is ever loaded to aggregate.
--
-- foreign_keys is ON (see internal/database/db.go), so rows cascade-delete with their
-- instance, like indexer_stat_counters. Counts are absolute cumulative values per
-- bucket; the flush adds deltas to them.
ALTER TABLE indexer_stat_counters ADD COLUMN grab_attempts INTEGER NOT NULL DEFAULT 0;

CREATE TABLE indexer_category_stats (
	instance_id   INTEGER NOT NULL REFERENCES indexer_instances(id) ON DELETE CASCADE,
	category_id   INTEGER NOT NULL,
	bucket        TEXT    NOT NULL,
	query_results INTEGER NOT NULL DEFAULT 0,
	grabs         INTEGER NOT NULL DEFAULT 0,
	updated_at    TEXT    NOT NULL,
	PRIMARY KEY (instance_id, category_id, bucket)
);
