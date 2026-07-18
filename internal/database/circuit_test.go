package database_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/autobrr/harbrr/internal/database"
)

func TestCircuitGetMissingRowIsZeroState(t *testing.T) {
	t.Parallel()
	db := openMigrated(t, filepath.Join(t.TempDir(), "circuit.db"))
	ctx := context.Background()
	id := seedInstance(t, db, "tt")

	got, err := database.Circuit{}.Get(ctx, db, id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	want := database.CircuitState{InstanceID: id}
	if got != want {
		t.Errorf("Get() = %+v, want zero state %+v", got, want)
	}
	if got.IsDisabled(time.Now()) {
		t.Error("zero state must not be disabled")
	}
}

func TestCircuitUpsertRoundTrips(t *testing.T) {
	t.Parallel()
	db := openMigrated(t, filepath.Join(t.TempDir(), "circuit.db"))
	ctx := context.Background()
	id := seedInstance(t, db, "tt")
	c := database.Circuit{}

	initial := time.Date(2026, time.June, 14, 12, 0, 0, 0, time.UTC)
	till := initial.Add(5 * time.Minute)
	state := database.CircuitState{
		InstanceID: id, EscalationLevel: 2, InitialFailure: initial, DisabledTill: till,
	}
	if err := c.Upsert(ctx, db, state); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	got, err := c.Get(ctx, db, id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.EscalationLevel != 2 {
		t.Errorf("EscalationLevel = %d, want 2", got.EscalationLevel)
	}
	if !got.InitialFailure.Equal(initial) {
		t.Errorf("InitialFailure = %v, want %v", got.InitialFailure, initial)
	}
	if !got.DisabledTill.Equal(till) {
		t.Errorf("DisabledTill = %v, want %v", got.DisabledTill, till)
	}
	if !got.IsDisabled(initial.Add(time.Minute)) {
		t.Error("expected disabled before DisabledTill")
	}
	if got.IsDisabled(till.Add(time.Second)) {
		t.Error("expected not disabled after DisabledTill")
	}

	// Re-upserting the closed (zero) state clears both timestamps back to NULL —
	// proving Upsert is a genuine overwrite, not an accumulate.
	if err := c.Upsert(ctx, db, database.CircuitState{InstanceID: id}); err != nil {
		t.Fatalf("upsert closed: %v", err)
	}
	closed, err := c.Get(ctx, db, id)
	if err != nil {
		t.Fatalf("get closed: %v", err)
	}
	if closed.EscalationLevel != 0 || !closed.InitialFailure.IsZero() || !closed.DisabledTill.IsZero() {
		t.Errorf("closed state = %+v, want all-zero", closed)
	}
}
