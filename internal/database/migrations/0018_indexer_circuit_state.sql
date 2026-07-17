-- 0017_indexer_circuit_state.sql — #253 consecutive-failure circuit breaker.
--
-- One row per instance holding its escalation ladder position (Prowlarr's
-- EscalationBackOff pattern): escalation_level climbs one rung per qualifying
-- failure and descends one rung per success (never a full reset — a flaky indexer
-- hovers rather than thrashing). disabled_till is the computed "excluded from
-- dispatch until" deadline; NULL means the circuit is closed (not disabled).
-- initial_failure marks the start of the current failure streak (NULL once the
-- streak clears), used to cap early-boot punishment during the startup grace
-- window. Absent row == level 0 / never disabled (the registry treats a missing
-- row as the zero state rather than pre-seeding one per instance).
--
-- foreign_keys is ON (see internal/database/db.go), so a row cascade-deletes when
-- its instance is removed, exactly like cache_counters. Timestamps are TEXT
-- RFC3339 (UTC), matching every other table.
CREATE TABLE indexer_circuit_state (
	instance_id      INTEGER PRIMARY KEY REFERENCES indexer_instances(id) ON DELETE CASCADE,
	escalation_level INTEGER NOT NULL DEFAULT 0,
	initial_failure  TEXT,
	disabled_till    TEXT
);
