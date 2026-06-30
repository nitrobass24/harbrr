package login

import (
	"sync"
	"testing"
)

// TestSolverUserAgent_ConcurrentAccessRaceFree drives the search/grab-path read
// (Session -> solverUA) concurrently with the login-path write (applySolveResult ->
// setSolverUA) to prove the RWMutex guard makes SolverUserAgent access race-free.
// Run under -race: without the guard the detector fires on the SolverUserAgent
// field, which is exactly the shared-cached-engine hazard (a relogin solve writing
// the UA while a concurrent search reads it via Session).
func TestSolverUserAgent_ConcurrentAccessRaceFree(t *testing.T) {
	t.Parallel()

	e := New() // installs a cookie jar + selector; no Client needed for this path

	const goroutines = 8
	const iters = 200

	var wg sync.WaitGroup
	for range goroutines {
		wg.Add(2)
		go func() {
			defer wg.Done()
			for range iters {
				// empty Cookies -> applySolveResult only sets the UA and returns,
				// so this exercises the guarded write without touching the jar.
				e.applySolveResult("https://tracker.example/", SolveResult{UserAgent: "UA/1.0"})
			}
		}()
		go func() {
			defer wg.Done()
			for range iters {
				_ = e.Session().UserAgent
			}
		}()
	}
	wg.Wait()

	if got := e.Session().UserAgent; got != "UA/1.0" {
		t.Fatalf("SolverUserAgent = %q, want UA/1.0 after concurrent solves", got)
	}
}
