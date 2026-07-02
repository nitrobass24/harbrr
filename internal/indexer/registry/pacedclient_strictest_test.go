package registry

import (
	"sync"
	"testing"
	"time"

	"golang.org/x/time/rate"
)

// TestLimiterForStrictestWinsConcurrent proves concurrent creators on one host
// converge on the STRICTEST (slowest) interval. The read-compare-set in limiterFor
// is serialized, so a looser SetLimit can't land last and lose the strictest value.
// Deterministic with the fix regardless of goroutine ordering; without it the final
// limit could settle on a non-strictest value.
func TestLimiterForStrictestWinsConcurrent(t *testing.T) {
	t.Parallel()

	const host = "strictest-wins.test"
	intervals := []time.Duration{
		200 * time.Millisecond,
		2 * time.Second, // strictest (slowest) -> smallest events/sec
		500 * time.Millisecond,
		time.Second,
	}

	var wg sync.WaitGroup
	for range 50 {
		for _, iv := range intervals {
			wg.Add(1)
			go func(iv time.Duration) {
				defer wg.Done()
				limiterFor(host, iv)
			}(iv)
		}
	}
	wg.Wait()

	v, ok := hostLimiters.Load(host)
	if !ok {
		t.Fatal("no limiter stored for host")
	}
	got := v.(*rate.Limiter).Limit()
	if want := rate.Every(2 * time.Second); got != want {
		t.Fatalf("limiter rate = %v, want strictest %v", got, want)
	}
}
