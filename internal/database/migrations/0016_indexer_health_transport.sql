-- 0016_indexer_health_transport.sql — #223 transport/transient health kind.
--
-- Widens the kind CHECK on indexer_health_events to also accept 'transport': connection
-- refused/reset, TLS/DNS failures, client timeouts, and EOF-after-200 reads all used to
-- fall through classifyHealth's default case and record no event, so a persistently
-- unreachable tracker read healthy. SQLite cannot ALTER a CHECK constraint in place, so
-- the table is rebuilt (create-new -> copy -> drop-old -> rename), same pattern as 0008.
-- No child table references indexer_health_events, so no ledger staging is needed here.

CREATE TABLE indexer_health_events_new (
	id          INTEGER PRIMARY KEY,
	instance_id INTEGER NOT NULL REFERENCES indexer_instances(id) ON DELETE CASCADE,
	kind        TEXT NOT NULL CHECK (kind IN ('auth_failure', 'rate_limited', 'parse_error', 'anti_bot', 'transport')),
	detail      TEXT,
	occurred_at TEXT NOT NULL
);

INSERT INTO indexer_health_events_new (id, instance_id, kind, detail, occurred_at)
SELECT id, instance_id, kind, detail, occurred_at FROM indexer_health_events;

DROP TABLE indexer_health_events;
ALTER TABLE indexer_health_events_new RENAME TO indexer_health_events;

CREATE INDEX indexer_health_events_instance_idx ON indexer_health_events (instance_id, occurred_at);
