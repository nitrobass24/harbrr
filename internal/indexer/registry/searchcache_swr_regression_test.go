package registry

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/mapper"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/normalizer"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
	"github.com/autobrr/harbrr/internal/web/torznab"
)

// gatedInner is a torznab.Indexer test double that blocks only its FIRST Search call
// on a gate (the SWR refresh), and serves a second, distinct result set immediately
// to every later call (the racing miss). It exists to drive the exact production
// window where a cache miss coalesces against an in-flight SWR refresh on the same
// cache key.
type gatedInner struct {
	calls     int64
	gate      chan struct{} // the first call blocks on this
	firstSet  []*normalizer.Release
	laterSet  []*normalizer.Release
	firstSeen chan struct{} // closed when the first (gated) call has entered Search
	once      sync.Once
}

func (g *gatedInner) Info() torznab.IndexerInfo          { return torznab.IndexerInfo{ID: "gated"} }
func (g *gatedInner) Capabilities() *mapper.Capabilities { return &mapper.Capabilities{} }
func (g *gatedInner) NeedsResolver() bool                { return false }
func (g *gatedInner) DownloadNeedsAuth() bool            { return false }

func (g *gatedInner) Grab(context.Context, string) (*search.GrabResult, error) {
	return nil, errors.New("not implemented")
}

func (g *gatedInner) Search(_ context.Context, _ search.Query) ([]*normalizer.Release, error) {
	n := atomic.AddInt64(&g.calls, 1)
	if n == 1 {
		g.once.Do(func() { close(g.firstSeen) })
		<-g.gate // the SWR refresh blocks here until released
		return g.firstSet, nil
	}
	return g.laterSet, nil
}

// TestMissCoalescingOntoInflightSWRReturnsReleases is the regression for the
// singleflight value-type collision between the miss path and the SWR path.
//
// Reproduction: prime the cache, advance past the refresh-ahead threshold so a hit
// fires an SWR refresh, hold that refresh in-flight (gated), then advance past expiry
// so the entry is gone and issue a concurrent search on the SAME key. Before the fix
// the miss coalesced onto the in-flight SWR flight (same singleflight key) and got a
// struct{}{} value, which the assertion to []*normalizer.Release dropped to nil — an
// empty result served as a successful search. After the fix the miss runs its own
// flight (swr-namespaced refresh key) and returns the real releases.
func TestMissCoalescingOntoInflightSWRReturnsReleases(t *testing.T) {
	t.Parallel()
	sc, instID, clk := testCache(t, keywordTTL, 80)

	// Two distinct, non-thin result sets so the entry takes the full 30m keyword TTL.
	primeSet := relSet("p1", "p2", "p3", "p4", "p5", "p6")
	missSet := relSet("m1", "m2", "m3", "m4", "m5", "m6")

	gate := make(chan struct{})
	inner := &gatedInner{
		gate:      gate,
		firstSet:  primeSet, // value the gated SWR refresh would store
		laterSet:  missSet,  // value the racing miss's own live fetch returns
		firstSeen: make(chan struct{}),
	}

	// Prime the cache with a normal miss using a separate, non-gated indexer so the
	// gatedInner's FIRST call is the SWR refresh, not the prime. We prime by storing
	// directly through a plain fakeInner-equivalent: reuse sc.search via a bare wrap.
	primer := &fakeInner{releases: primeSet}
	primeIdx := sc.wrap(primer, instID, nil)
	q := search.Query{Keywords: "swr"}
	if _, err := primeIdx.Search(context.Background(), q); err != nil {
		t.Fatalf("prime: %v", err)
	}

	// Now wrap the gated indexer for the refresh + miss phase.
	idx := sc.wrap(inner, instID, nil)

	// Advance past 80% of the 30m TTL (24m) but before expiry, then take a hit. The
	// hit serves the cached prime value and fires the gated SWR refresh in background.
	advance(clk, 25*time.Minute)
	hit, err := idx.Search(context.Background(), q)
	if err != nil {
		t.Fatalf("stale hit: %v", err)
	}
	if len(hit) != 6 || hit[0].Title != "p1" {
		t.Fatalf("stale hit served %+v, want cached prime", hit)
	}

	// Wait until the SWR refresh has actually entered the (gated) live Search, so the
	// flight is genuinely in flight before the miss races it.
	select {
	case <-inner.firstSeen:
	case <-time.After(2 * time.Second):
		t.Fatal("SWR refresh never started")
	}

	// Advance past expiry: the entry is gone, so the next request is a true miss.
	advance(clk, 10*time.Minute) // now 35m > 30m TTL

	// Issue the racing miss on the SAME cache key while the SWR refresh is still
	// gated. It must return the real releases from its OWN live fetch, never nil, and
	// must NOT block on the gated SWR flight. Run it in a goroutine with a bounded
	// wait: under the bug the miss coalesces onto the gated SWR singleflight and
	// blocks indefinitely, so a timeout here is itself a failure signal.
	type missResult struct {
		releases []*normalizer.Release
		err      error
	}
	done := make(chan missResult, 1)
	go func() {
		r, e := idx.Search(context.Background(), q)
		done <- missResult{releases: r, err: e}
	}()

	select {
	case res := <-done:
		if res.err != nil {
			t.Fatalf("racing miss errored instead of serving live: %v", res.err)
		}
		if len(res.releases) == 0 {
			t.Fatal("BUG: racing miss coalesced onto in-flight SWR and got nil/empty releases")
		}
		if res.releases[0].Title != "m1" {
			t.Fatalf("racing miss served %+v, want its own live miss set", res.releases)
		}
	case <-time.After(3 * time.Second):
		close(gate) // unblock the SWR goroutine before failing
		t.Fatal("BUG: racing miss blocked on the in-flight SWR flight (same singleflight key)")
	}

	// Release the gated SWR refresh so the goroutine can finish cleanly.
	close(gate)
}

// TestSWRRefreshUsesSeparateSingleflightKey is a unit-level guard that the refresh
// key is namespaced away from the bare cache key, so the two flights can never share
// a value. It does not depend on timing.
func TestSWRRefreshUsesSeparateSingleflightKey(t *testing.T) {
	t.Parallel()
	const key = "abc123"
	if got := swrKey(key); got == key {
		t.Fatalf("swrKey(%q) = %q, must differ from the bare cache key", key, got)
	}
}
