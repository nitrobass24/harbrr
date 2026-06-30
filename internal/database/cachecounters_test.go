package database_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/autobrr/harbrr/internal/database"
)

// TestCacheCountersUpsertAndAll proves Upsert inserts a row then overwrites it in
// place with ABSOLUTE values (no accumulation), and AllCounters round-trips every
// row ordered by instance id.
func TestCacheCountersUpsertAndAll(t *testing.T) {
	t.Parallel()

	db := openMigrated(t, filepath.Join(t.TempDir(), "harbrr.db"))
	ctx := context.Background()
	store := database.CacheCountersStore{}
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)

	id1 := insertInstance(t, db, "one")
	id2 := insertInstance(t, db, "two")

	// Insert both instances' first snapshot.
	for _, c := range []database.CacheCounter{
		{InstanceID: id1, Hits: 5, Misses: 2, Suppressed: 1, UpdatedAt: now},
		{InstanceID: id2, Hits: 3, Misses: 9, Suppressed: 0, UpdatedAt: now},
	} {
		if err := store.Upsert(ctx, db, c); err != nil {
			t.Fatalf("Upsert(%d): %v", c.InstanceID, err)
		}
	}

	// Re-upsert id1 with larger absolute values: the row is overwritten, not added to.
	if err := store.Upsert(ctx, db,
		database.CacheCounter{InstanceID: id1, Hits: 11, Misses: 4, Suppressed: 2, UpdatedAt: now.Add(time.Minute)}); err != nil {
		t.Fatalf("Upsert update: %v", err)
	}

	got, err := store.AllCounters(ctx, db)
	if err != nil {
		t.Fatalf("AllCounters: %v", err)
	}
	want := []database.CacheCounter{
		{InstanceID: id1, Hits: 11, Misses: 4, Suppressed: 2},
		{InstanceID: id2, Hits: 3, Misses: 9, Suppressed: 0},
	}
	if len(got) != len(want) {
		t.Fatalf("AllCounters len = %d, want %d", len(got), len(want))
	}
	for i, w := range want {
		if got[i].InstanceID != w.InstanceID || got[i].Hits != w.Hits ||
			got[i].Misses != w.Misses || got[i].Suppressed != w.Suppressed {
			t.Errorf("row %d = %+v, want hits/misses/suppressed %d/%d/%d for instance %d",
				i, got[i], w.Hits, w.Misses, w.Suppressed, w.InstanceID)
		}
	}
}

// TestCacheCountersCascadeDelete proves a counter row is removed when its instance is
// deleted (FK ON DELETE CASCADE), mirroring search_cache.
func TestCacheCountersCascadeDelete(t *testing.T) {
	t.Parallel()

	db := openMigrated(t, filepath.Join(t.TempDir(), "harbrr.db"))
	ctx := context.Background()
	store := database.CacheCountersStore{}

	id := insertInstance(t, db, "doomed")
	if err := store.Upsert(ctx, db,
		database.CacheCounter{InstanceID: id, Hits: 7, UpdatedAt: time.Now()}); err != nil {
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

// TestCacheCountersUpsertDanglingInstance proves an Upsert for a non-existent instance
// is rejected by the FK — the behavior FlushCounters tolerates per-row for an instance
// deleted out from under an in-memory counter.
func TestCacheCountersUpsertDanglingInstance(t *testing.T) {
	t.Parallel()

	db := openMigrated(t, filepath.Join(t.TempDir(), "harbrr.db"))
	ctx := context.Background()

	err := database.CacheCountersStore{}.Upsert(ctx, db,
		database.CacheCounter{InstanceID: 9999, Hits: 1, UpdatedAt: time.Now()})
	if err == nil {
		t.Fatal("Upsert with dangling instance_id succeeded, want foreign-key violation")
	}
}
