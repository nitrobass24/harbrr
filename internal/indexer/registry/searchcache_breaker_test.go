package registry

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
	"github.com/autobrr/harbrr/internal/indexer/core"
)

// breakerTTL is keywordTTL with the negative-result breaker armed (60s window).
var breakerTTL = ttlConfig{
	rss: 5 * time.Minute, keyword: 30 * time.Minute, thin: 2 * time.Minute,
	thinThreshold: 5, negative: time.Minute,
}

// TestBreakerShortCircuitsGenericError proves that once a live search errors, the
// next MISS for that instance is served the recorded error without re-driving the
// tracker — and that the breaker self-heals once its window lapses.
func TestBreakerShortCircuitsGenericError(t *testing.T) {
	t.Parallel()
	sc, instID, clk := testCache(t, breakerTTL, 0)
	sentinel := errors.New("tracker down")
	inner := &fakeInner{err: sentinel}
	idx := sc.probe(inner, instID, nil)
	q := search.Query{Keywords: "x"}

	// First search drives the tracker, errors, and trips the breaker.
	if _, err := idx.Search(context.Background(), q); !errors.Is(err, sentinel) {
		t.Fatalf("first err = %v, want sentinel", err)
	}
	// Second search within the window short-circuits: the tracker is NOT hit again,
	// and the recorded error is replayed.
	if _, err := idx.Search(context.Background(), q); !errors.Is(err, sentinel) {
		t.Fatalf("second err = %v, want replayed sentinel", err)
	}
	if got := inner.callCount(); got != 1 {
		t.Fatalf("inner called %d times, want 1 (second short-circuited)", got)
	}
	if _, _, sup := sc.instanceSnapshot(instID); sup != 1 {
		t.Fatalf("breaker suppressed = %d, want 1", sup)
	}

	// Past the window the breaker probes live again — recover and serve.
	advance(clk, time.Minute+time.Second)
	inner.mu.Lock()
	inner.err = nil
	inner.releases = relSet("OK")
	inner.mu.Unlock()
	got, err := idx.Search(context.Background(), q)
	if err != nil {
		t.Fatalf("post-window search: %v", err)
	}
	if len(got) != 1 || got[0].Title != "OK" {
		t.Fatalf("post-window = %+v, want live OK", got)
	}
	if c := inner.callCount(); c != 2 {
		t.Fatalf("inner called %d times, want 2 (1 trip + 1 probe)", c)
	}
}

// TestBreakerHonorsRetryAfter proves a rate-limit response holds the breaker for at
// least its Retry-After even when that exceeds the configured negative_ttl window.
func TestBreakerHonorsRetryAfter(t *testing.T) {
	t.Parallel()
	sc, instID, clk := testCache(t, breakerTTL, 0) // negative window is 1m
	rl := &search.RateLimitedError{StatusCode: 429, RetryAfter: 3 * time.Minute}
	inner := &fakeInner{err: rl}
	idx := sc.probe(inner, instID, nil)
	q := search.Query{Keywords: "y"}

	if _, err := idx.Search(context.Background(), q); !errors.Is(err, search.ErrRateLimited) {
		t.Fatalf("first err = %v, want rate-limited", err)
	}
	// At 2m (> negative 1m, < Retry-After 3m) the breaker is still open.
	advance(clk, 2*time.Minute)
	if _, err := idx.Search(context.Background(), q); !errors.Is(err, search.ErrRateLimited) {
		t.Fatalf("at 2m err = %v, want still rate-limited (Retry-After honored)", err)
	}
	if got := inner.callCount(); got != 1 {
		t.Fatalf("inner called %d times at 2m, want 1 (still suppressed)", got)
	}
	// Past Retry-After the breaker probes live again.
	advance(clk, 90*time.Second) // now 3m30s total
	inner.mu.Lock()
	inner.err = nil
	inner.releases = relSet("Z")
	inner.mu.Unlock()
	if _, err := idx.Search(context.Background(), q); err != nil {
		t.Fatalf("post Retry-After: %v", err)
	}
	if got := inner.callCount(); got != 2 {
		t.Fatalf("inner called %d times, want 2 (probe after Retry-After)", got)
	}
}

// TestBreakerServesFreshCacheWhileOpen proves an open breaker never blanks out a
// still-fresh positive entry: only a MISS consults the breaker.
func TestBreakerServesFreshCacheWhileOpen(t *testing.T) {
	t.Parallel()
	sc, instID, _ := testCache(t, breakerTTL, 0)
	inner := &fakeInner{releases: relSet("Cached", "Cached2", "Cached3", "Cached4", "Cached5", "Cached6")}
	idx := sc.probe(inner, instID, nil)
	qa := search.Query{Keywords: "a"}

	// Prime a successful entry for query A.
	if _, err := idx.Search(context.Background(), qa); err != nil {
		t.Fatalf("prime A: %v", err)
	}
	// A different query B errors and trips the instance breaker.
	inner.mu.Lock()
	inner.err = errors.New("down")
	inner.mu.Unlock()
	if _, err := idx.Search(context.Background(), search.Query{Keywords: "b"}); err == nil {
		t.Fatal("query B should have errored")
	}
	// Query A is still a fresh positive hit — served from cache, not short-circuited.
	got, err := idx.Search(context.Background(), qa)
	if err != nil {
		t.Fatalf("query A while breaker open: %v", err)
	}
	if len(got) != 6 || got[0].Title != "Cached" {
		t.Fatalf("query A served %+v, want cached set", got)
	}
}

// TestBreakerBypassForcesLive proves a CacheBypass request ignores an open breaker
// and drives the tracker (operator override).
func TestBreakerBypassForcesLive(t *testing.T) {
	t.Parallel()
	sc, instID, _ := testCache(t, breakerTTL, 0)
	inner := &fakeInner{err: errors.New("down")}
	idx := sc.probe(inner, instID, nil)
	q := search.Query{Keywords: "x"}

	// Trip the breaker.
	if _, err := idx.Search(context.Background(), q); err == nil {
		t.Fatal("want trip error")
	}
	// Recover the tracker and force a live request despite the open breaker.
	inner.mu.Lock()
	inner.err = nil
	inner.releases = relSet("Live")
	inner.mu.Unlock()
	got, err := idx.Search(core.WithCacheBypass(context.Background()), q)
	if err != nil {
		t.Fatalf("bypass: %v", err)
	}
	if len(got) != 1 || got[0].Title != "Live" {
		t.Fatalf("bypass served %+v, want live", got)
	}
	if c := inner.callCount(); c != 2 {
		t.Fatalf("inner called %d times, want 2 (trip + bypass)", c)
	}
}

// TestBreakerDisabledWhenNegativeZero proves negative_ttl=0 leaves the breaker inert:
// every errored search re-drives the tracker (legacy behavior).
func TestBreakerDisabledWhenNegativeZero(t *testing.T) {
	t.Parallel()
	sc, instID, _ := testCache(t, keywordTTL, 0) // keywordTTL has negative=0
	inner := &fakeInner{err: errors.New("down")}
	idx := sc.probe(inner, instID, nil)
	q := search.Query{Keywords: "x"}

	for range 3 {
		if _, err := idx.Search(context.Background(), q); err == nil {
			t.Fatal("want error each time")
		}
	}
	if got := inner.callCount(); got != 3 {
		t.Fatalf("inner called %d times, want 3 (breaker inert)", got)
	}
}

// TestBreakerRuntimeDisableIsImmediate proves setting negative_ttl=0 at runtime stops
// suppression at once, even with a window already open.
func TestBreakerRuntimeDisableIsImmediate(t *testing.T) {
	t.Parallel()
	sc, instID, _ := testCache(t, breakerTTL, 0)
	inner := &fakeInner{err: errors.New("down")}
	idx := sc.probe(inner, instID, nil)
	q := search.Query{Keywords: "x"}
	ctx := context.Background()

	// Trip the breaker, then confirm it is suppressing.
	if _, err := idx.Search(ctx, q); err == nil {
		t.Fatal("want trip error")
	}
	if _, err := idx.Search(ctx, q); err == nil {
		t.Fatal("want suppressed error")
	}
	if got := inner.callCount(); got != 1 {
		t.Fatalf("inner called %d times, want 1 (suppressing)", got)
	}

	// Disable the breaker at runtime; the open window must stop suppressing at once.
	zero := time.Duration(0)
	if _, err := sc.UpdateConfig(ctx, CacheConfigPatch{NegativeTTL: &zero}); err != nil {
		t.Fatalf("UpdateConfig: %v", err)
	}
	inner.mu.Lock()
	inner.err = nil
	inner.releases = relSet("OK")
	inner.mu.Unlock()
	if _, err := idx.Search(ctx, q); err != nil {
		t.Fatalf("post-disable search: %v", err)
	}
	if got := inner.callCount(); got != 2 {
		t.Fatalf("inner called %d times, want 2 (breaker disabled, probed live)", got)
	}
}

// TestTripBreakerSkipsCallerCancel proves a caller-cancelled context never trips the
// breaker (the consumer aborted; the tracker did not fail).
func TestTripBreakerSkipsCallerCancel(t *testing.T) {
	t.Parallel()
	sc, instID, _ := testCache(t, breakerTTL, 0)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	sc.tripBreaker(ctx, instID, errors.New("aborted"))
	if err := sc.breaker.replay(instID, sc.clock()); err != nil {
		t.Fatalf("breaker tripped on caller-cancel: %v", err)
	}
}

// TestTripBreakerSkipsFollowerInheritedCancel proves the singleflight FOLLOWER path:
// a caller whose OWN context is still live (Background) but whose error is the LEADER's
// request-aborted error WRAPPING context.Canceled must NOT trip the breaker. Otherwise
// one disconnected client opens the breaker instance-wide for ~one window. The existing
// same-ctx test only covers a caller cancelling its own context, which gives false
// confidence about this path.
func TestTripBreakerSkipsFollowerInheritedCancel(t *testing.T) {
	t.Parallel()
	sc, instID, _ := testCache(t, breakerTTL, 0)
	// The follower's own ctx is alive; only the inherited error carries the cancel.
	inherited := fmt.Errorf("registry: request aborted: %w", context.Canceled)
	sc.tripBreaker(context.Background(), instID, inherited)
	if err := sc.breaker.replay(instID, sc.clock()); err != nil {
		t.Fatalf("breaker tripped on follower-inherited caller-cancel: %v", err)
	}
}

// TestBreakerNotTrippedByInheritedCancelOnSearch drives the follower scenario through
// the real search path: a live (Background) context plus an inner error wrapping
// context.Canceled (as a singleflight follower would receive from a cancelled leader)
// leaves the breaker closed, so the very next consumer still probes the tracker live
// instead of being suppressed.
//
// With U8R-F5 the live-ctx follower now also re-runs its OWN live search when the flight
// returns an inherited context error, so the FIRST search calls inner twice (the coalesced
// flight + the fallback) before surfacing the still-cancelling error; the recovery search
// is the third call. The breaker staying closed — proven by the recover-and-serve — is the
// invariant under test.
func TestBreakerNotTrippedByInheritedCancelOnSearch(t *testing.T) {
	t.Parallel()
	sc, instID, _ := testCache(t, breakerTTL, 0)
	inner := &fakeInner{err: fmt.Errorf("registry: request aborted: %w", context.Canceled)}
	idx := sc.probe(inner, instID, nil)
	q := search.Query{Keywords: "x"}

	if _, err := idx.Search(context.Background(), q); !errors.Is(err, context.Canceled) {
		t.Fatalf("first search err = %v, want the inherited cancel", err)
	}
	// The breaker must be closed: the recover-and-serve below proves a live probe.
	inner.mu.Lock()
	inner.err = nil
	inner.releases = relSet("OK")
	inner.mu.Unlock()
	got, err := idx.Search(context.Background(), q)
	if err != nil {
		t.Fatalf("second search (breaker must be closed): %v", err)
	}
	if len(got) != 1 || got[0].Title != "OK" {
		t.Fatalf("second search = %+v, want live OK (not suppressed)", got)
	}
	if c := inner.callCount(); c != 3 {
		t.Fatalf("inner called %d times, want 3 (flight + U8R-F5 fallback, then recovery; cancel did not trip the breaker)", c)
	}
}

// TestClassifyBreakerError covers the trip decision and window selection.
func TestClassifyBreakerError(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	tests := []struct {
		name      string
		err       error
		neg       time.Duration
		wantTrip  bool
		wantUntil time.Time
	}{
		{"nil error", nil, time.Minute, false, time.Time{}},
		{"disabled window", errors.New("down"), 0, false, time.Time{}},
		{"generic uses window", errors.New("down"), time.Minute, true, now.Add(time.Minute)},
		{
			"rate-limit longer retry-after wins",
			&search.RateLimitedError{StatusCode: 429, RetryAfter: 3 * time.Minute},
			time.Minute, true, now.Add(3 * time.Minute),
		},
		{
			"rate-limit shorter retry-after keeps window",
			&search.RateLimitedError{StatusCode: 503, RetryAfter: 10 * time.Second},
			time.Minute, true, now.Add(time.Minute),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			until, ok := classifyBreakerError(tt.err, tt.neg, now)
			if ok != tt.wantTrip {
				t.Fatalf("trip = %v, want %v", ok, tt.wantTrip)
			}
			if ok && !until.Equal(tt.wantUntil) {
				t.Fatalf("until = %v, want %v", until, tt.wantUntil)
			}
		})
	}
}

// TestPerInstanceCountersIsolate proves hits/misses are tracked per instance.
func TestPerInstanceCountersIsolate(t *testing.T) {
	t.Parallel()
	sc, instID, _ := testCache(t, breakerTTL, 0)
	inner := &fakeInner{releases: relSet("A")}
	idx := sc.probe(inner, instID, nil)
	q := search.Query{Keywords: "a"}
	ctx := context.Background()

	if _, err := idx.Search(ctx, q); err != nil { // miss
		t.Fatal(err)
	}
	if _, err := idx.Search(ctx, q); err != nil { // hit
		t.Fatal(err)
	}
	hits, misses, sup := sc.instanceSnapshot(instID)
	if hits != 1 || misses != 1 || sup != 0 {
		t.Fatalf("instance counters = %d/%d/%d, want 1/1/0", hits, misses, sup)
	}
	// An unseen instance reports zeroes.
	if h, m, s := sc.instanceSnapshot(instID + 999); h != 0 || m != 0 || s != 0 {
		t.Fatalf("unseen instance = %d/%d/%d, want zeroes", h, m, s)
	}
}
