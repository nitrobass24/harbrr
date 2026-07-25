package backup

import (
	"context"
	"strings"
	"testing"

	"github.com/autobrr/harbrr/internal/database"
)

// TestRestorePriorityDefaultsOldShapeBundle proves loadInstances normalizes a bundled
// instance's zero-value Priority (an old-shape InstanceRow written before #364 added the
// field never carried one, so it decodes as the JSON zero value) to restoreDefaultPriority
// — a restored fleet must not re-push every indexer to the apps at an invalid priority 0.
func TestRestorePriorityDefaultsOldShapeBundle(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	db, err := database.Open(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	svc := &Service{db: db}
	tables := &Tables{
		IndexerInstances: []InstanceRow{
			// Old-shape row: no Priority/MinSeeders set (the JSON zero value, as a bundle
			// exported before #364 would decode).
			{ID: 1, Slug: "tt", DefinitionID: "tt", Name: "TT", Enabled: true, Protocol: "torrent"},
			// New-shape row: an explicit non-default priority survives untouched.
			{ID: 2, Slug: "tt2", DefinitionID: "tt2", Name: "TT2", Enabled: true, Protocol: "torrent", Priority: 10, MinSeeders: 5},
		},
	}
	if err := svc.restore(ctx, tables, true); err != nil {
		t.Fatalf("restore: %v", err)
	}

	list, err := (database.Instances{}).List(ctx, db)
	if err != nil {
		t.Fatalf("list restored instances: %v", err)
	}
	byslug := map[string]int{}
	for _, inst := range list {
		if inst.Slug == "tt" {
			if inst.Priority != restoreDefaultPriority {
				t.Errorf("old-shape instance priority = %d, want %d (default)", inst.Priority, restoreDefaultPriority)
			}
			if inst.MinSeeders != 0 {
				t.Errorf("old-shape instance minSeeders = %d, want 0", inst.MinSeeders)
			}
		}
		if inst.Slug == "tt2" {
			if inst.Priority != 10 {
				t.Errorf("new-shape instance priority = %d, want 10 (carried through)", inst.Priority)
			}
			if inst.MinSeeders != 5 {
				t.Errorf("new-shape instance minSeeders = %d, want 5 (carried through)", inst.MinSeeders)
			}
		}
		byslug[inst.Slug]++
	}
	if byslug["tt"] != 1 || byslug["tt2"] != 1 {
		t.Fatalf("restored instances = %+v, want exactly one each of tt, tt2", byslug)
	}
}

// TestRestorePriorityOutOfRangeRejected proves a malformed/hand-edited bundle's
// out-of-range Priority (not the pre-#364 zero value, which defaults instead) is
// rejected rather than silently persisted — this restore path has no DB CHECK or
// registry-side guard behind it.
func TestRestorePriorityOutOfRangeRejected(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	for name, priority := range map[string]int{"too low": -1, "too high": 999} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			db, err := database.Open(":memory:")
			if err != nil {
				t.Fatalf("open db: %v", err)
			}
			t.Cleanup(func() { _ = db.Close() })
			if err := db.Migrate(ctx); err != nil {
				t.Fatalf("migrate: %v", err)
			}

			svc := &Service{db: db}
			tables := &Tables{
				IndexerInstances: []InstanceRow{
					{ID: 1, Slug: "bad-priority", DefinitionID: "bad-priority", Name: "Bad", Enabled: true, Protocol: "torrent", Priority: priority},
				},
			}
			err = svc.restore(ctx, tables, true)
			if err == nil {
				t.Fatal("restore with an out-of-range priority succeeded, want an error")
			}
			if !strings.Contains(err.Error(), "bad-priority") {
				t.Errorf("error = %v, want it to name the offending slug", err)
			}
		})
	}
}

// TestRestoreMinSeedersNegativeRejected proves a malformed/hand-edited bundle's
// negative MinSeeders is rejected rather than silently persisted.
func TestRestoreMinSeedersNegativeRejected(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	db, err := database.Open(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	svc := &Service{db: db}
	tables := &Tables{
		IndexerInstances: []InstanceRow{
			{ID: 1, Slug: "bad-minseeders", DefinitionID: "bad-minseeders", Name: "Bad", Enabled: true, Protocol: "torrent", MinSeeders: -3},
		},
	}
	if err := svc.restore(ctx, tables, true); err == nil {
		t.Fatal("restore with a negative minSeeders succeeded, want an error")
	} else if !strings.Contains(err.Error(), "bad-minseeders") {
		t.Errorf("error = %v, want it to name the offending slug", err)
	}
}
