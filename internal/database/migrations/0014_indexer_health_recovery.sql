-- 0014_indexer_health_recovery.sql — durable recovery watermark for indexer health.
--
-- indexer_health_events remains an append-only failure ledger: its rows still back
-- recent-event detail and cumulative failure stats. A successful explicit indexer
-- test records which failure id it recovered through, allowing the status endpoint
-- to turn healthy immediately without deleting history. occurred_at is retained as
-- a fallback ordering signal if SQLite ever reuses row ids after retention empties
-- the failure table.

CREATE TABLE indexer_health_recovery (
	instance_id      INTEGER PRIMARY KEY REFERENCES indexer_instances(id) ON DELETE CASCADE,
	through_event_id INTEGER NOT NULL,
	occurred_at      TEXT NOT NULL
);
