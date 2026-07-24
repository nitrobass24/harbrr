package registry

import (
	"context"
	"sync"
	"testing"

	"github.com/autobrr/harbrr/internal/database"
)

// fakeInvalidator is a test double for the invalidator seam that records every call
// so Delete's eviction fan-out (autobrr/harbrr#345) can be asserted directly.
type fakeInvalidator struct {
	mu                    sync.Mutex
	invalidated           []string
	invalidatedSearchIDs  []int64
	forgotCacheCounterIDs []int64
	forgotStatsIDs        []int64
	forgotBudgetIDs       []int64
}

func (f *fakeInvalidator) invalidate(slug string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.invalidated = append(f.invalidated, slug)
}

func (f *fakeInvalidator) invalidateSearchCache(_ context.Context, id int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.invalidatedSearchIDs = append(f.invalidatedSearchIDs, id)
}

func (f *fakeInvalidator) forgetCacheCounters(id int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.forgotCacheCounterIDs = append(f.forgotCacheCounterIDs, id)
}

func (f *fakeInvalidator) forgetStats(id int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.forgotStatsIDs = append(f.forgotStatsIDs, id)
}

func (f *fakeInvalidator) forgetBudget(id int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.forgotBudgetIDs = append(f.forgotBudgetIDs, id)
}

// TestDeleteInvalidatesSearchCache proves Manager.Delete routes through
// invalidateSearchCache (in addition to the pre-existing invalidate/forget* calls) so a
// deleted instance's search-cache epoch is bumped and its rows purged — closing the
// rowid-reuse write-back poisoning gap (autobrr/harbrr#345).
func TestDeleteInvalidatesSearchCache(t *testing.T) {
	t.Parallel()
	db, err := database.Open(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	instID := insertTestInstance(t, db)
	inv := &fakeInvalidator{}
	mgr := &Manager{db: db, instances: database.Instances{}, inv: inv}

	if err := mgr.Delete(context.Background(), "fake"); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	if got := inv.invalidated; len(got) != 1 || got[0] != "fake" {
		t.Fatalf("invalidate calls = %v, want [\"fake\"]", got)
	}
	if got := inv.invalidatedSearchIDs; len(got) != 1 || got[0] != instID {
		t.Fatalf("invalidateSearchCache calls = %v, want [%d]", got, instID)
	}
	if got := inv.forgotCacheCounterIDs; len(got) != 1 || got[0] != instID {
		t.Fatalf("forgetCacheCounters calls = %v, want [%d]", got, instID)
	}
	if got := inv.forgotStatsIDs; len(got) != 1 || got[0] != instID {
		t.Fatalf("forgetStats calls = %v, want [%d]", got, instID)
	}
	if got := inv.forgotBudgetIDs; len(got) != 1 || got[0] != instID {
		t.Fatalf("forgetBudget calls = %v, want [%d]", got, instID)
	}
}
