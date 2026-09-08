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
// (autobrr/harbrr#345) can be asserted directly. What forgetInstance itself evicts is
// asserted against the real Resolver in forgetinstances_test.go.
type fakeCleanup struct {
	mu          sync.Mutex
	invalidated []string
	forgotIDs   []int64
}

func (f *fakeCleanup) invalidate(slug string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.invalidated = append(f.invalidated, slug)
}

func (f *fakeCleanup) invalidateSearchCache(_ context.Context, _ int64) {}

func (f *fakeCleanup) forgetInstance(_ context.Context, id int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.forgotIDs = append(f.forgotIDs, id)
}

// TestDeleteForgetsInstance proves Manager.Delete evicts the resolver's built engine
// for the slug AND routes the deleted instance's id through forgetInstance — the
// cleanup that bumps the search-cache epoch and drops the counters, stats, budget and
// diagnostics keyed to it, closing the rowid-reuse write-back poisoning gap
// (autobrr/harbrr#345).
func TestDeleteForgetsInstance(t *testing.T) {
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
	if got := inv.forgotIDs; len(got) != 1 || got[0] != instID {
		t.Fatalf("forgetInstance calls = %v, want [%d]", got, instID)
	}
}
