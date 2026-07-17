-- 0018_indexer_budget_counters.sql — durable per-indexer request-budget counters
-- (autobrr/harbrr#251).
--
-- One row per instance holding the query/grab counters for the CURRENT rolling
-- period (a UTC calendar day or hour, per the instance's `limits_unit` setting), plus
-- an "exhausted" latch per kind used by the reactive quota-learning path: when a
-- tracker responds with its own declared daily-quota error (e.g. dognzb's newznab
-- code 910), the registry marks that kind exhausted for the rest of the period even
-- though it may not know the tracker's exact numeric cap.
--
-- Counts are ABSOLUTE (not deltas) and written with an idempotent UPSERT, exactly
-- like cache_counters / indexer_stat_counters. *_period stores the period key the
-- counts apply to (e.g. "2026-07-17" or "2026-07-17T14"); the registry compares it
-- against the current period at read time and resets the count/exhausted flag on
-- rollover rather than relying on a background sweep.
--
-- foreign_keys is ON (see internal/database/db.go), so a row cascade-deletes when its
-- instance is removed. updated_at is TEXT RFC3339 (UTC).
CREATE TABLE indexer_budget_counters (
	instance_id     INTEGER PRIMARY KEY REFERENCES indexer_instances(id) ON DELETE CASCADE,
	query_period    TEXT    NOT NULL DEFAULT '',
	query_count     INTEGER NOT NULL DEFAULT 0,
	query_exhausted INTEGER NOT NULL DEFAULT 0,
	grab_period     TEXT    NOT NULL DEFAULT '',
	grab_count      INTEGER NOT NULL DEFAULT 0,
	grab_exhausted  INTEGER NOT NULL DEFAULT 0,
	updated_at      TEXT    NOT NULL
);
