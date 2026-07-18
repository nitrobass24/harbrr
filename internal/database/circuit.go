package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/autobrr/harbrr/internal/database/dbinterface"
)

// Circuit is the SQLite repository for the per-instance circuit-breaker row
// (indexer_circuit_state). Stateless: every method takes an Execer and routes
// placeholders through q.Rebind, mirroring Health/CacheCountersStore.
type Circuit struct{}

// CircuitState is one instance's escalation-ladder position. EscalationLevel 0
// with a zero DisabledTill is the closed (never disabled) state. InitialFailure
// marks the start of the current failure streak (zero once it clears).
type CircuitState struct {
	InstanceID      int64
	EscalationLevel int
	InitialFailure  time.Time
	DisabledTill    time.Time
}

// IsDisabled reports whether the circuit is open at now (excluded from dispatch).
func (s CircuitState) IsDisabled(now time.Time) bool {
	return !s.DisabledTill.IsZero() && s.DisabledTill.After(now)
}

// Get returns instanceID's circuit state. An instance with no row (never failed)
// yields the zero state at level 0, not an error — mirroring Health.Recovery.
func (Circuit) Get(ctx context.Context, q dbinterface.Execer, instanceID int64) (CircuitState, error) {
	var (
		s              = CircuitState{InstanceID: instanceID}
		initialFailure sql.NullString
		disabledTill   sql.NullString
	)
	err := q.QueryRowContext(ctx,
		q.Rebind(`SELECT escalation_level, initial_failure, disabled_till
		 FROM indexer_circuit_state WHERE instance_id = ?`),
		instanceID).Scan(&s.EscalationLevel, &initialFailure, &disabledTill)
	if errors.Is(err, sql.ErrNoRows) {
		return CircuitState{InstanceID: instanceID}, nil
	}
	if err != nil {
		return CircuitState{}, fmt.Errorf("database: read circuit state for instance %d: %w", instanceID, err)
	}
	if initialFailure.Valid {
		s.InitialFailure = parseTime(initialFailure.String)
	}
	if disabledTill.Valid {
		s.DisabledTill = parseTime(disabledTill.String)
	}
	return s, nil
}

// Upsert writes instanceID's circuit state, overwriting any previous row. A zero
// InitialFailure/DisabledTill stores NULL (the closed state), matching Get's
// interpretation of an absent value.
func (Circuit) Upsert(ctx context.Context, q dbinterface.Execer, s CircuitState) error {
	_, err := q.ExecContext(ctx,
		q.Rebind(`INSERT INTO indexer_circuit_state (instance_id, escalation_level, initial_failure, disabled_till)
			VALUES (?, ?, ?, ?)
			ON CONFLICT(instance_id) DO UPDATE SET
			  escalation_level = excluded.escalation_level,
			  initial_failure = excluded.initial_failure,
			  disabled_till = excluded.disabled_till`),
		s.InstanceID, s.EscalationLevel, nullableTime(s.InitialFailure), nullableTime(s.DisabledTill))
	if err != nil {
		return fmt.Errorf("database: upsert circuit state for instance %d: %w", s.InstanceID, err)
	}
	return nil
}
