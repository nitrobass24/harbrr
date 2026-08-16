package database_test

import (
	"context"
	"testing"
	"time"

	"github.com/autobrr/harbrr/internal/database"
	"github.com/autobrr/harbrr/internal/database/dbtest"
)

// TestIndexerCategoryStatsAddDeltas proves the write is ADDITIVE per (instance,
// category, bucket) — a second flush of the same pair accumulates rather than
// overwriting — and that Tallies sums a category across buckets in one read.
func TestIndexerCategoryStatsAddDeltas(t *testing.T) {
	t.Parallel()

	db := dbtest.OpenMigrated(t)
	ctx := context.Background()
	store := database.IndexerCategoryStatsStore{}
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	id1 := insertInstance(t, db, "one")
	id2 := insertInstance(t, db, "two")

	june, may := database.MonthBucket(now), database.MonthBucket(now.AddDate(0, -1, 0))
	if failed, err := store.AddDeltas(ctx, db, may, []database.IndexerCategoryCount{
		{InstanceID: id1, CategoryID: 2000, QueryResults: 10, Grabs: 1},
	}, now); err != nil || len(failed) != 0 {
		t.Fatalf("AddDeltas(may) = %v / %v, want no failures", failed, err)
	}
	if failed, err := store.AddDeltas(ctx, db, june, []database.IndexerCategoryCount{
		{InstanceID: id1, CategoryID: 2000, QueryResults: 5, Grabs: 2},
		{InstanceID: id1, CategoryID: 3000, QueryResults: 7},
		{InstanceID: id2, CategoryID: 0, QueryResults: 1},
	}, now); err != nil || len(failed) != 0 {
		t.Fatalf("AddDeltas(june) = %v / %v, want no failures", failed, err)
	}
	// The same bucket again: accumulates on top of what is stored.
	if _, err := store.AddDeltas(ctx, db, june, []database.IndexerCategoryCount{
		{InstanceID: id1, CategoryID: 2000, QueryResults: 5, Grabs: 3},
	}, now); err != nil {
		t.Fatalf("AddDeltas(june again): %v", err)
	}

	got, err := store.Tallies(ctx, db)
	if err != nil {
		t.Fatalf("Tallies: %v", err)
	}
	want := []database.IndexerCategoryCount{
		{InstanceID: id1, CategoryID: 2000, QueryResults: 20, Grabs: 6}, // 10 + 5 + 5 over two buckets
		{InstanceID: id1, CategoryID: 3000, QueryResults: 7},
		{InstanceID: id2, CategoryID: 0, QueryResults: 1},
	}
	if len(got) != len(want) {
		t.Fatalf("Tallies = %+v, want %+v", got, want)
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("tally[%d] = %+v, want %+v", i, got[i], w)
		}
	}
}

// TestIndexerCategoryStatsAddDeltasReportsFailures proves a dangling instance id (an
// instance deleted between the record and the flush) is DISCARDED — a foreign-key
// violation is terminal, and retrying it would re-fail every flush forever — while
// its siblings still commit, without raising an error for the expected case.
func TestIndexerCategoryStatsAddDeltasReportsFailures(t *testing.T) {
	t.Parallel()

	db := dbtest.OpenMigrated(t)
	ctx := context.Background()
	store := database.IndexerCategoryStatsStore{}
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	id := insertInstance(t, db, "one")

	failed, err := store.AddDeltas(ctx, db, database.MonthBucket(now), []database.IndexerCategoryCount{
		{InstanceID: 9999, CategoryID: 2000, QueryResults: 3}, // no such instance: FK violation
		{InstanceID: id, CategoryID: 5000, QueryResults: 4},
	}, now)
	if err != nil {
		t.Fatalf("AddDeltas: a dangling instance is expected on deletion races, not an error: %v", err)
	}
	if len(failed) != 0 {
		t.Errorf("failed = %+v, want none — an FK-terminal delta must be discarded, not retried", failed)
	}
	got, err := store.Tallies(ctx, db)
	if err != nil {
		t.Fatalf("Tallies: %v", err)
	}
	if len(got) != 1 || got[0].InstanceID != id || got[0].QueryResults != 4 {
		t.Errorf("Tallies = %+v, want the sibling row to have committed", got)
	}
}

// TestIndexerCategoryStatsDeleteBefore proves retention is a range delete of whole
// month buckets, keeping the boundary month itself.
func TestIndexerCategoryStatsDeleteBefore(t *testing.T) {
	t.Parallel()

	db := dbtest.OpenMigrated(t)
	ctx := context.Background()
	store := database.IndexerCategoryStatsStore{}
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	id := insertInstance(t, db, "one")

	for _, months := range []int{0, -1, -13} {
		bucket := database.MonthBucket(now.AddDate(0, months, 0))
		if _, err := store.AddDeltas(ctx, db, bucket, []database.IndexerCategoryCount{
			{InstanceID: id, CategoryID: 2000, QueryResults: 1},
		}, now); err != nil {
			t.Fatalf("seed %s: %v", bucket, err)
		}
	}

	cutoff := database.MonthBucket(now.AddDate(0, -12, 0))
	deleted, err := store.DeleteBefore(ctx, db, cutoff)
	if err != nil {
		t.Fatalf("DeleteBefore: %v", err)
	}
	if deleted != 1 {
		t.Errorf("deleted = %d, want 1 (only the 13-month-old bucket)", deleted)
	}
	got, err := store.Tallies(ctx, db)
	if err != nil {
		t.Fatalf("Tallies: %v", err)
	}
	if len(got) != 1 || got[0].QueryResults != 2 {
		t.Errorf("Tallies = %+v, want the two in-window buckets summed to 2", got)
	}
}

// TestCategoryStatsRetentionMonths proves the setting round-trips and that anything
// missing, unparseable or out of range falls back to the default rather than wedging
// the reaper with a nonsense window.
func TestCategoryStatsRetentionMonths(t *testing.T) {
	t.Parallel()

	db := dbtest.OpenMigrated(t)
	ctx := context.Background()
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)

	if got, err := database.CategoryStatsRetentionMonths(ctx, db); err != nil || got != database.DefaultCategoryStatsRetentionMonths {
		t.Fatalf("unset retention = %d / %v, want the default", got, err)
	}
	if err := database.SetCategoryStatsRetentionMonths(ctx, db, 6, now); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if got, err := database.CategoryStatsRetentionMonths(ctx, db); err != nil || got != 6 {
		t.Fatalf("retention = %d / %v, want 6", got, err)
	}

	for _, stored := range []string{"", "twelve", "0", "999"} {
		if err := (database.AppSettings{}).Set(ctx, db, "stats_category_retention_months", stored, now); err != nil {
			t.Fatalf("seed %q: %v", stored, err)
		}
		if got, err := database.CategoryStatsRetentionMonths(ctx, db); err != nil || got != database.DefaultCategoryStatsRetentionMonths {
			t.Errorf("retention for stored %q = %d / %v, want the default", stored, got, err)
		}
	}
}
