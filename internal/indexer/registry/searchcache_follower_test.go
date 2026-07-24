package registry

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/mapper"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/normalizer"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
	"github.com/autobrr/harbrr/internal/indexer/core"
)

// seqInner is a core.Indexer test double whose FIRST Search returns firstErr and
// every subsequent Search returns results. It models the serveMiss branch a singleflight
// FOLLOWER lands on: the coalesced flight returned the leader's error (call 1), then the
// follower's own fallback live search succeeds (call 2). Deterministic — no goroutines or
// timing — so it is the authoritative FAIL-BEFORE / PASS-AFTER proof for U8R-F5.
type seqInner struct {
	calls    int64
	firstErr error
	results  []*normalizer.Release
}

func (s *seqInner) Info() core.IndexerInfo             { return core.IndexerInfo{ID: "seq"} }
func (s *seqInner) Capabilities() *mapper.Capabilities { return &mapper.Capabilities{} }
func (s *seqInner) NeedsResolver() bool                { return false }
func (s *seqInner) DownloadNeedsAuth() bool            { return false }
func (s *seqInner) SupportsOffsetPaging() bool         { return false }
func (s *seqInner) ConsumesSearchMode() bool           { return false }

func (s *seqInner) Grab(context.Context, string) (*search.GrabResult, error) {
	return nil, errors.New("not implemented")
}

func (s *seqInner) Search(_ context.Context, _ search.Query) ([]*normalizer.Release, error) {
	if atomic.AddInt64(&s.calls, 1) == 1 {
		return nil, s.firstErr
	}
	return s.results, nil
}

func (s *seqInner) callCount() int64 { return atomic.LoadInt64(&s.calls) }

// TestServeMissFollowerRecoversFromInheritedLeaderError is the core U8R-F5 regression. A
// coalesced follower whose OWN context is still live must not surface an error inherited
// from the flight leader's aborted request: when the flight returns a context error and
// our ctx is live, serveMiss falls back to the follower's own live search.
//
// FAIL-BEFORE (`return nil, err`): the follower returns the leader's context error and
// inner is called once. PASS-AFTER (the fallback): the follower runs its own search
// (inner call 2) and returns its own results. A non-context error (a genuine tracker
// failure) must still propagate — the fallback is scoped to inherited context errors.
func TestServeMissFollowerRecoversFromInheritedLeaderError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		flightErr  error
		wantResult bool  // true: fall back to our own results; false: propagate flightErr
		wantErr    error // sentinel to match when wantResult is false
	}{
		{
			name:       "leader client disconnect (context.Canceled) -> fall back",
			flightErr:  context.Canceled,
			wantResult: true,
		},
		{
			name:       "leader request deadline (context.DeadlineExceeded) -> fall back",
			flightErr:  context.DeadlineExceeded,
			wantResult: true,
		},
		{
			name:       "wrapped leader cancel -> fall back (errors.Is unwraps)",
			flightErr:  fmt.Errorf("indexer seq: fetch: %w", context.Canceled),
			wantResult: true,
		},
		{
			name:      "genuine tracker error -> propagate (not a context error)",
			flightErr: errors.New("tracker returned 503"),
			wantErr:   errors.New("tracker returned 503"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			sc, instID, _ := testCache(t, keywordTTL, 0)
			inner := &seqInner{firstErr: tt.flightErr, results: relSet("f1", "f2")}
			idx := sc.probe(inner, instID, nil)
			q := search.Query{Keywords: "follower"}

			// The follower's OWN context is live throughout.
			got, err := idx.Search(context.Background(), q)

			if tt.wantResult {
				if err != nil {
					t.Fatalf("follower returned error %v, want its own fresh results", err)
				}
				if len(got) != 2 || got[0].Title != "f1" {
					t.Fatalf("follower served %+v, want its own results f1/f2", got)
				}
				if c := inner.callCount(); c != 2 {
					t.Fatalf("inner called %d times, want 2 (inherited-error flight + own fallback)", c)
				}
				return
			}

			// A non-context error must reach the caller unchanged and must NOT trigger a
			// second (fallback) live search.
			if err == nil || err.Error() != tt.wantErr.Error() {
				t.Fatalf("returned err %v, want %v propagated", err, tt.wantErr)
			}
			if got != nil {
				t.Fatalf("returned releases %+v on error, want nil", got)
			}
			if c := inner.callCount(); c != 1 {
				t.Fatalf("inner called %d times on a non-context error, want 1 (no fallback)", c)
			}
		})
	}
}

// cancelOnSearchInner cancels the caller's context on its first Search and returns the
// resulting cancellation, modeling a FOLLOWER whose OWN request is aborted mid-flight.
// serveMiss must then return the cancellation and must NOT run a fallback search — a real
// client-gone must never be masked with fresh results. Any second call (which the guard
// must prevent) returns results so a regression that wrongly falls back leaks them.
type cancelOnSearchInner struct {
	calls   int64
	cancel  context.CancelFunc
	results []*normalizer.Release
}

func (c *cancelOnSearchInner) Info() core.IndexerInfo {
	return core.IndexerInfo{ID: "cancel"}
}
func (c *cancelOnSearchInner) Capabilities() *mapper.Capabilities { return &mapper.Capabilities{} }
func (c *cancelOnSearchInner) NeedsResolver() bool                { return false }
func (c *cancelOnSearchInner) DownloadNeedsAuth() bool            { return false }
func (c *cancelOnSearchInner) SupportsOffsetPaging() bool         { return false }
func (c *cancelOnSearchInner) ConsumesSearchMode() bool           { return false }

func (c *cancelOnSearchInner) Grab(context.Context, string) (*search.GrabResult, error) {
	return nil, errors.New("not implemented")
}

func (c *cancelOnSearchInner) Search(_ context.Context, _ search.Query) ([]*normalizer.Release, error) {
	if atomic.AddInt64(&c.calls, 1) == 1 {
		c.cancel() // the follower's OWN request is aborted while the fetch is in flight
		return nil, context.Canceled
	}
	return c.results, nil
}

func (c *cancelOnSearchInner) callCount() int64 { return atomic.LoadInt64(&c.calls) }

// TestServeMissReturnsOwnCancellation is the guard for U8R-F5: when the FOLLOWER's OWN
// context is cancelled, serveMiss must return the cancellation rather than fall back to a
// fresh search — the request is genuinely gone. The context is live at the outer Fetch
// (so we reach serveMiss) and is cancelled during the flight, so the ctx.Err() != nil
// guard is what forces the error to be returned.
func TestServeMissReturnsOwnCancellation(t *testing.T) {
	t.Parallel()
	sc, instID, _ := testCache(t, keywordTTL, 0)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	inner := &cancelOnSearchInner{cancel: cancel, results: relSet("leaked")}
	idx := sc.probe(inner, instID, nil)

	got, err := idx.Search(ctx, search.Query{Keywords: "gone"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("returned err %v, want context.Canceled (real client-gone must not be masked)", err)
	}
	if got != nil {
		t.Fatalf("returned releases %+v, want nil (no fallback for a cancelled own ctx)", got)
	}
	if c := inner.callCount(); c != 1 {
		t.Fatalf("inner called %d times, want 1 (the guard must skip the fallback search)", c)
	}
}

// coalesceInner blocks its FIRST (leader) Search on leaderRelease and then returns the
// leader ctx's error; every later (follower fallback) Search returns followerResults. It
// drives the realistic end-to-end coalescing scenario in TestSingleflightFollower...
//
// The SECOND call (the follower retry flight — autobrr/harbrr#342) optionally blocks on
// retryRelease too, signaling retrySeen on entry: this lets a test hold the retry flight
// open long enough for a SECOND follower to coalesce onto it (proving retryMissFlight
// re-coalesces surviving followers instead of each running its own independent live
// search). Both fields are nil in the original single-follower test, where the second
// call must return immediately — a nil retryRelease skips the gate entirely.
type coalesceInner struct {
	calls           int64
	firstSeen       chan struct{}
	firstOnce       sync.Once
	leaderRelease   chan struct{}
	retrySeen       chan struct{}
	retryOnce       sync.Once
	retryRelease    chan struct{}
	followerResults []*normalizer.Release
}

func (c *coalesceInner) Info() core.IndexerInfo {
	return core.IndexerInfo{ID: "coalesce"}
}
func (c *coalesceInner) Capabilities() *mapper.Capabilities { return &mapper.Capabilities{} }
func (c *coalesceInner) NeedsResolver() bool                { return false }
func (c *coalesceInner) DownloadNeedsAuth() bool            { return false }
func (c *coalesceInner) SupportsOffsetPaging() bool         { return false }
func (c *coalesceInner) ConsumesSearchMode() bool           { return false }

func (c *coalesceInner) Grab(context.Context, string) (*search.GrabResult, error) {
	return nil, errors.New("not implemented")
}

func (c *coalesceInner) callCount() int64 { return atomic.LoadInt64(&c.calls) }

func (c *coalesceInner) Search(ctx context.Context, _ search.Query) ([]*normalizer.Release, error) {
	n := atomic.AddInt64(&c.calls, 1)
	if n == 1 {
		c.firstOnce.Do(func() { close(c.firstSeen) })
		<-c.leaderRelease
		return nil, ctx.Err() // the leader's context error (its client went away)
	}
	if n == 2 && c.retryRelease != nil {
		c.retryOnce.Do(func() { close(c.retrySeen) })
		<-c.retryRelease
	}
	return c.followerResults, nil
}

// TestSingleflightFollowerSurvivesLeaderCancel exercises the real coalescing path: two
// requests collapse onto one flight; the LEADER's client disconnects mid-fetch while the
// FOLLOWER's context stays live. The follower must recover with its own fresh results,
// and the leader must still surface its own cancellation.
//
// Determinism: the leader's live fetch parks on leaderRelease, so its flight stays open
// until the test closes it. The follower is launched only after the leader is confirmed
// in-flight (firstSeen); waitForMisses(2) confirms the follower has reached serveMiss's
// miss counter, and the yields let it enter the still-open flight, so coalescing is
// guaranteed before leaderRelease closes.
func TestSingleflightFollowerSurvivesLeaderCancel(t *testing.T) {
	t.Parallel()
	sc, instID, _ := testCache(t, keywordTTL, 0)

	inner := &coalesceInner{
		firstSeen:       make(chan struct{}),
		leaderRelease:   make(chan struct{}),
		followerResults: relSet("f1", "f2"),
	}
	idx := sc.probe(inner, instID, nil)
	q := search.Query{Keywords: "coalesce"}

	leaderCtx, cancelLeader := context.WithCancel(context.Background())
	defer cancelLeader()

	var (
		leaderErr      error
		leaderReleases []*normalizer.Release
		leaderDone     = make(chan struct{})
	)
	go func() {
		defer close(leaderDone)
		leaderReleases, leaderErr = idx.Search(leaderCtx, q)
	}()

	// Wait until the leader is inside the live fetch (flight registered and held open).
	select {
	case <-inner.firstSeen:
	case <-time.After(2 * time.Second):
		t.Fatal("leader never entered the live search")
	}

	var (
		followerErr      error
		followerReleases []*normalizer.Release
		followerDone     = make(chan struct{})
	)
	go func() {
		defer close(followerDone)
		followerReleases, followerErr = idx.Search(context.Background(), q)
	}()

	// The follower has reached serveMiss (misses: leader + follower == 2); yield so it
	// enters the leader's still-open flight and coalesces before we release the leader.
	waitForMisses(t, sc, 2)
	for range 100 {
		runtime.Gosched()
	}

	// The leader's client goes away: cancel its ctx, then release its blocked fetch so it
	// returns context.Canceled into the shared flight. The coalesced follower inherits it
	// — but the follower's OWN ctx is live.
	cancelLeader()
	close(inner.leaderRelease)

	<-followerDone
	<-leaderDone

	if followerErr != nil {
		t.Fatalf("follower returned error %v, want its own fresh results", followerErr)
	}
	if len(followerReleases) != 2 || followerReleases[0].Title != "f1" {
		t.Fatalf("follower served %+v, want its own results f1/f2", followerReleases)
	}
	if !errors.Is(leaderErr, context.Canceled) {
		t.Fatalf("leader returned %v (releases %+v), want context.Canceled", leaderErr, leaderReleases)
	}
}

// TestFollowersCoalesceOnLeaderCancel extends TestSingleflightFollowerSurvivesLeaderCancel
// to TWO followers (autobrr/harbrr#342): the leader's client disconnects mid-fetch, and
// BOTH followers inherit the cancellation while their own contexts stay live.
// retryMissFlight's job is to re-coalesce every surviving follower onto ONE fresh flight
// rather than each running its own independent live search — this proves that: the
// tracker is hit exactly twice total (the leader's original attempt + ONE retry), never
// three times (a follower stampede), and both followers still get fresh results.
//
// Determinism mirrors the single-follower test: the leader's live fetch parks on
// leaderRelease so its flight stays open until released; the retry flight (inner's
// second call) similarly parks on retryRelease so BOTH followers have time to coalesce
// onto it before it completes.
func TestFollowersCoalesceOnLeaderCancel(t *testing.T) {
	t.Parallel()
	sc, instID, _ := testCache(t, keywordTTL, 0)

	inner := &coalesceInner{
		firstSeen:       make(chan struct{}),
		leaderRelease:   make(chan struct{}),
		retrySeen:       make(chan struct{}),
		retryRelease:    make(chan struct{}),
		followerResults: relSet("f1", "f2"),
	}
	idx := sc.probe(inner, instID, nil)
	q := search.Query{Keywords: "stampede"}

	leaderCtx, cancelLeader := context.WithCancel(context.Background())
	defer cancelLeader()

	var (
		leaderErr  error
		leaderDone = make(chan struct{})
	)
	go func() {
		defer close(leaderDone)
		_, leaderErr = idx.Search(leaderCtx, q)
	}()

	select {
	case <-inner.firstSeen:
	case <-time.After(2 * time.Second):
		t.Fatal("leader never entered the live search")
	}

	type followerOutcome struct {
		releases []*normalizer.Release
		err      error
	}
	const numFollowers = 2
	followerDone := make([]chan followerOutcome, numFollowers)
	for i := range followerDone {
		ch := make(chan followerOutcome, 1)
		followerDone[i] = ch
		go func() {
			r, e := idx.Search(context.Background(), q)
			ch <- followerOutcome{releases: r, err: e}
		}()
	}

	// Both followers have reached serveMiss (leader + 2 followers == 3 misses); yield so
	// they enter the leader's still-open flight and coalesce before it is released.
	waitForMisses(t, sc, 1+numFollowers)
	for range 100 {
		runtime.Gosched()
	}

	// The leader's client goes away: cancel its ctx, then release its blocked fetch so it
	// returns context.Canceled into the shared flight, inherited by both followers.
	cancelLeader()
	close(inner.leaderRelease)
	<-leaderDone
	if !errors.Is(leaderErr, context.Canceled) {
		t.Fatalf("leader returned %v, want context.Canceled", leaderErr)
	}

	// Both followers must now re-coalesce onto ONE fresh retry flight instead of each
	// running an independent live search. Wait for the retry to actually start, yield so
	// BOTH followers have a chance to reach the retry's sf.Do before it completes, then
	// release it.
	select {
	case <-inner.retrySeen:
	case <-time.After(2 * time.Second):
		t.Fatal("retry flight never started")
	}
	for range 100 {
		runtime.Gosched()
	}
	close(inner.retryRelease)

	for i, ch := range followerDone {
		select {
		case res := <-ch:
			if res.err != nil {
				t.Fatalf("follower %d returned error %v, want its own fresh results", i, res.err)
			}
			if len(res.releases) != 2 || res.releases[0].Title != "f1" {
				t.Fatalf("follower %d served %+v, want f1/f2", i, res.releases)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("follower %d never returned", i)
		}
	}

	if c := inner.callCount(); c != 2 {
		t.Fatalf("inner called %d times, want 2 (leader + ONE retry flight, not a follower stampede)", c)
	}
}

// waitForMisses blocks until the cache's cumulative miss counter reaches want or a
// timeout fires. It gives a deterministic barrier for "N requests have reached serveMiss"
// (the miss counter is bumped at serveMiss entry, immediately before the singleflight).
func waitForMisses(t *testing.T, sc *SearchCache, want int64) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if sc.misses.Load() >= want {
			return
		}
		runtime.Gosched()
	}
	t.Fatalf("misses = %d, never reached %d", sc.misses.Load(), want)
}
