package database_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/autobrr/harbrr/internal/database"
)

// TestIndexerStatCountersUpsertAndAll proves Upsert inserts then overwrites a row in
// place with ABSOLUTE values, AllCounters round-trips every row ordered by instance id,
// and the nullable last-* timestamps round-trip (including NULL when never observed).
func TestIndexerStatCountersUpsertAndAll(t *testing.T) {
	t.Parallel()

	db := openMigrated(t, filepath.Join(t.TempDir(), "harbrr.db"))
	ctx := context.Background()
	store := database.IndexerStatCountersStore{}
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	lastQuery := now.Add(-time.Minute)
	lastGrab := now.Add(-2 * time.Minute)

	id1 := insertInstance(t, db, "one")
	id2 := insertInstance(t, db, "two")

	for _, c := range []database.IndexerStatCounter{
		// id1 has both timestamps set.
		{InstanceID: id1, Queries: 5, Grabs: 2, ResponseMsTotal: 500, LastQueryAt: lastQuery, LastGrabAt: lastGrab, UpdatedAt: now},
		// id2 was queried but never grabbed (last_grab_at stays NULL).
		{InstanceID: id2, Queries: 3, Grabs: 0, ResponseMsTotal: 90, LastQueryAt: lastQuery, UpdatedAt: now},
	} {
		if err := store.Upsert(ctx, db, c); err != nil {
			t.Fatalf("Upsert(%d): %v", c.InstanceID, err)
		}
	}

	// Re-upsert id1 with larger absolute values: overwritten, not accumulated.
	newLastQuery := now.Add(time.Minute)
	if err := store.Upsert(ctx, db, database.IndexerStatCounter{
		InstanceID: id1, Queries: 11, Grabs: 4, ResponseMsTotal: 1100,
		LastQueryAt: newLastQuery, LastGrabAt: lastGrab, UpdatedAt: now.Add(time.Minute),
	}); err != nil {
		t.Fatalf("Upsert update: %v", err)
	}

	got, err := store.AllCounters(ctx, db)
	if err != nil {
		t.Fatalf("AllCounters: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("AllCounters len = %d, want 2", len(got))
	}

	// id1: absolute overwrite with both timestamps.
	if r := got[0]; r.InstanceID != id1 || r.Queries != 11 || r.Grabs != 4 || r.ResponseMsTotal != 1100 {
		t.Errorf("row0 = %+v, want id1 11/4/1100", r)
	}
	if !got[0].LastQueryAt.Equal(newLastQuery) {
		t.Errorf("id1 lastQueryAt = %v, want %v", got[0].LastQueryAt, newLastQuery)
	}
	if !got[0].LastGrabAt.Equal(lastGrab) {
		t.Errorf("id1 lastGrabAt = %v, want %v", got[0].LastGrabAt, lastGrab)
	}
	// id2: never grabbed -> zero (NULL) last_grab_at.
	if r := got[1]; r.InstanceID != id2 || r.Queries != 3 || r.Grabs != 0 || r.ResponseMsTotal != 90 {
		t.Errorf("row1 = %+v, want id2 3/0/90", r)
	}
	if !got[1].LastGrabAt.IsZero() {
		t.Errorf("id2 lastGrabAt = %v, want zero (never grabbed)", got[1].LastGrabAt)
	}
	if got[1].LastQueryAt.IsZero() {
		t.Error("id2 lastQueryAt is zero, want the set timestamp")
	}
}

// TestIndexerStatCountersCascadeDelete proves a counter row is removed when its instance
// is deleted (FK ON DELETE CASCADE).
func TestIndexerStatCountersCascadeDelete(t *testing.T) {
	t.Parallel()

	db := openMigrated(t, filepath.Join(t.TempDir(), "harbrr.db"))
	ctx := context.Background()
	store := database.IndexerStatCountersStore{}

	id := insertInstance(t, db, "doomed")
	if err := store.Upsert(ctx, db,
		database.IndexerStatCounter{InstanceID: id, Queries: 7, UpdatedAt: time.Now()}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if _, err := db.ExecContext(ctx, "DELETE FROM indexer_instances WHERE id = ?", id); err != nil {
		t.Fatalf("delete instance: %v", err)
	}

	got, err := store.AllCounters(ctx, db)
	if err != nil {
		t.Fatalf("AllCounters: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("counters after cascade delete = %d, want 0", len(got))
	}
}

// TestIndexerStatCountersUpsertDanglingInstance proves an Upsert for a non-existent
// instance is rejected by the FK — the behavior FlushCounters tolerates per-row.
func TestIndexerStatCountersUpsertDanglingInstance(t *testing.T) {
	t.Parallel()

	db := openMigrated(t, filepath.Join(t.TempDir(), "harbrr.db"))
	ctx := context.Background()

	err := database.IndexerStatCountersStore{}.Upsert(ctx, db,
		database.IndexerStatCounter{InstanceID: 9999, Queries: 1, UpdatedAt: time.Now()})
	if err == nil {
		t.Fatal("Upsert with dangling instance_id succeeded, want foreign-key violation")
	}
}
