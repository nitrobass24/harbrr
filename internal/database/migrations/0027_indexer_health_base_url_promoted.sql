-- 0027_indexer_health_base_url_promoted.sql — #375 base-URL failover promotions.
--
-- Widens the kind CHECK on indexer_health_events to also accept 'base_url_promoted':
-- the automatic base-URL failover moving an indexer onto another host its definition
-- already lists. It is the one kind in this table that is NOT a failure — it shares the
-- table because this table IS the indexer's visible timeline, and a failover that left
-- no trace would be exactly the silent self-healing the feature must not be. Everything
-- that folds these rows as failures (the per-kind tally and last-failure timestamp in
-- database.applyHealthCount) skips it.
--
-- SQLite cannot ALTER a CHECK constraint in place, so the table is rebuilt
-- (create-new -> copy -> drop-old -> rename), same pattern as 0016. No child table
-- references indexer_health_events, so no ledger staging is needed here.

CREATE TABLE indexer_health_events_new (
	id          INTEGER PRIMARY KEY,
	instance_id INTEGER NOT NULL REFERENCES indexer_instances(id) ON DELETE CASCADE,
	kind        TEXT NOT NULL CHECK (kind IN ('auth_failure', 'rate_limited', 'parse_error', 'anti_bot', 'transport', 'base_url_promoted')),
	detail      TEXT,
	occurred_at TEXT NOT NULL
);

INSERT INTO indexer_health_events_new (id, instance_id, kind, detail, occurred_at)
SELECT id, instance_id, kind, detail, occurred_at FROM indexer_health_events;

DROP TABLE indexer_health_events;
ALTER TABLE indexer_health_events_new RENAME TO indexer_health_events;

CREATE INDEX indexer_health_events_instance_idx ON indexer_health_events (instance_id, occurred_at);
