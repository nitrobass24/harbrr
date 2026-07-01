package registry

import (
	"path/filepath"
	"sync"
	"testing"
)

// TestForgetInstanceConcurrent is a -race smoke: ForgetInstance runs concurrently
// with increments on the same instance without data-racing or panicking. The fix
// (LoadAndDelete + atomic Swap to capture-and-zero) makes the prune safe against a
// concurrent counters()/increment; this exercises that path and asserts the global
// never goes negative.
func TestForgetInstanceConcurrent(t *testing.T) {
	t.Parallel()

	db := openCacheDB(t, filepath.Join(t.TempDir(), "harbrr.db"))
	id := insertInstanceSlug(t, db, "racy")
	sc := newCacheOn(db)

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for range 2000 {
			ic := sc.counters(id)
			ic.hits.Add(1)
			sc.hits.Add(1)
		}
	}()
	go func() {
		defer wg.Done()
		for range 50 {
			sc.ForgetInstance(id)
		}
	}()
	wg.Wait()

	if got := sc.hits.Load(); got < 0 {
		t.Fatalf("global hits went negative: %d", got)
	}
}
