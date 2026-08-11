package registry

import (
	"context"
	"sync"
	"testing"

	"github.com/autobrr/harbrr/internal/database"
	"github.com/autobrr/harbrr/internal/database/dbtest"
)

// fakeCleanup is a test double satisfying BOTH post-mutation seams (serveEvicter and
// instanceForgetter), recording every call so Delete's cleanup fan-out
// (autobrr/harbrr#345) can be asserted directly.
type fakeCleanup struct {
	mu                    sync.Mutex
	invalidated           []string
	invalidatedSearchIDs  []int64
	forgotCacheCounterIDs []int64
	forgotStatsIDs        []int64
	forgotBudgetIDs       []int64
	forgotDiagnosticsIDs  []int64
}

func (f *fakeCleanup) invalidate(slug string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.invalidated = append(f.invalidated, slug)
}

func (f *fakeCleanup) invalidateSearchCache(_ context.Context, id int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.invalidatedSearchIDs = append(f.invalidatedSearchIDs, id)
}

func (f *fakeCleanup) forgetCacheCounters(id int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.forgotCacheCounterIDs = append(f.forgotCacheCounterIDs, id)
}

func (f *fakeCleanup) forgetStats(id int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.forgotStatsIDs = append(f.forgotStatsIDs, id)
}

func (f *fakeCleanup) forgetBudget(id int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.forgotBudgetIDs = append(f.forgotBudgetIDs, id)
}

func (f *fakeCleanup) forgetDiagnostics(id int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.forgotDiagnosticsIDs = append(f.forgotDiagnosticsIDs, id)
}

// TestDeleteInvalidatesSearchCache proves Manager.Delete routes through
// invalidateSearchCache (in addition to the pre-existing invalidate/forget* calls) so a
// deleted instance's search-cache epoch is bumped and its rows purged — closing the
// rowid-reuse write-back poisoning gap (autobrr/harbrr#345).
func TestDeleteInvalidatesSearchCache(t *testing.T) {
	t.Parallel()
	db := dbtest.OpenMigrated(t)
	instID := insertTestInstance(t, db)
	inv := &fakeCleanup{}
	mgr := &Manager{db: db, instances: database.Instances{}, evicter: inv, forgetter: inv}

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
	// A deleted indexer's captured failed fetches go with it (autobrr/harbrr#390).
	if got := inv.forgotDiagnosticsIDs; len(got) != 1 || got[0] != instID {
		t.Fatalf("forgetDiagnostics calls = %v, want [%d]", got, instID)
	}
}
