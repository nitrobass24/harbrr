package backup

import (
	"context"
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
