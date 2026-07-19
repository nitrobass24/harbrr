package database_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/autobrr/harbrr/internal/database"
)

// TestBudgetCountersGetAndUpsert proves Get reports found=false for an instance never
// touched, Upsert writes ABSOLUTE values (not deltas), a re-Upsert overwrites in place
// (including flipping the exhausted latches), and the boolean columns round-trip.
func TestBudgetCountersGetAndUpsert(t *testing.T) {
	t.Parallel()

	db := openMigrated(t, filepath.Join(t.TempDir(), "harbrr.db"))
	ctx := context.Background()
	store := database.BudgetCountersStore{}
	now := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)

	id := insertInstance(t, db, "budgeted")

	if _, found, err := store.Get(ctx, db, id); err != nil {
		t.Fatalf("Get(never touched): %v", err)
	} else if found {
		t.Fatal("Get(never touched) reported found=true")
	}

	row := database.BudgetCounter{
		InstanceID: id, QueryPeriod: "2026-07-17", QueryCount: 5, QueryExhausted: false,
		GrabPeriod: "2026-07-17", GrabCount: 1, GrabExhausted: false, UpdatedAt: now,
	}
	if err := store.Upsert(ctx, db, row); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	got, found, err := store.Get(ctx, db, id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !found {
		t.Fatal("Get reported found=false after Upsert")
	}
	if got.QueryCount != 5 || got.GrabCount != 1 || got.QueryExhausted || got.GrabExhausted {
		t.Fatalf("Get = %+v, want QueryCount=5 GrabCount=1 both exhausted=false", got)
	}
	if !got.UpdatedAt.Equal(now) {
		t.Fatalf("UpdatedAt = %v, want %v", got.UpdatedAt, now)
	}

	// Re-upsert with the query kind now exhausted (the reactive-learning latch) and a
	// larger absolute grab count: the row overwrites in place, not accumulates.
	row.QueryExhausted = true
	row.GrabCount = 9
	if err := store.Upsert(ctx, db, row); err != nil {
		t.Fatalf("re-Upsert: %v", err)
	}
	got, _, err = store.Get(ctx, db, id)
	if err != nil {
		t.Fatalf("Get after re-Upsert: %v", err)
	}
	if !got.QueryExhausted {
		t.Fatal("QueryExhausted did not persist as true")
	}
	if got.GrabCount != 9 {
		t.Fatalf("GrabCount = %d, want 9 (absolute overwrite, not accumulated)", got.GrabCount)
	}
}

// TestBudgetCountersCascadeDelete proves a deleted instance's budget counter row is
// removed via ON DELETE CASCADE, mirroring cache_counters/indexer_stat_counters.
func TestBudgetCountersCascadeDelete(t *testing.T) {
	t.Parallel()

	db := openMigrated(t, filepath.Join(t.TempDir(), "harbrr.db"))
	ctx := context.Background()
	store := database.BudgetCountersStore{}
	now := time.Now().UTC()

	id := insertInstance(t, db, "to-delete")
	if err := store.Upsert(ctx, db, database.BudgetCounter{InstanceID: id, UpdatedAt: now}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if _, err := db.ExecContext(ctx, db.Rebind(`DELETE FROM indexer_instances WHERE id = ?`), id); err != nil {
		t.Fatalf("delete instance: %v", err)
	}
	if _, found, err := store.Get(ctx, db, id); err != nil {
		t.Fatalf("Get after cascade delete: %v", err)
	} else if found {
		t.Fatal("budget counter row survived the instance's cascade delete")
	}
}
