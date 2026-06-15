-- 0002_indexer_health.sql — Phase 6 per-indexer health events.
--
-- Append-only. occurred_at is TEXT RFC3339 (UTC), the universal convention.
-- kind is one of: auth_failure | rate_limited | parse_error | anti_bot.
-- detail is a credential-scrubbed message (internal/http.RedactError) — a passkey,
-- cookie, or token never reaches this column. Rows cascade-delete with their
-- parent instance (foreign_keys is ON; see internal/database/db.go).
CREATE TABLE indexer_health_events (
	id          INTEGER PRIMARY KEY,
	instance_id INTEGER NOT NULL REFERENCES indexer_instances(id) ON DELETE CASCADE,
	kind        TEXT NOT NULL,
	detail      TEXT,
	occurred_at TEXT NOT NULL
);

CREATE INDEX indexer_health_events_instance_idx ON indexer_health_events (instance_id, occurred_at);
