package registry

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/autobrr/harbrr/internal/database"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/mapper"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/normalizer"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
	"github.com/autobrr/harbrr/internal/web/torznabhttp"
)

// fakeInner is a torznabhttp.Indexer test double. It counts Search calls, can block on
// a gate (to exercise singleflight), and can return a fixed error or release set.
type fakeInner struct {
	mu       sync.Mutex
	calls    int64
	releases []*normalizer.Release
	err      error
	gate     chan struct{} // when non-nil, Search blocks until it is closed

	// firstSeen, when non-nil, is closed once as the first Search call enters (before
	// it blocks on the gate). It lets a test wait deterministically for the gated
	// flight to be in progress instead of sleeping a fixed duration.
	firstSeen chan struct{}
	firstOnce sync.Once
}

func (f *fakeInner) Info() torznabhttp.IndexerInfo      { return torznabhttp.IndexerInfo{ID: "fake"} }
func (f *fakeInner) Capabilities() *mapper.Capabilities { return &mapper.Capabilities{} }
func (f *fakeInner) NeedsResolver() bool                { return false }
func (f *fakeInner) DownloadNeedsAuth() bool            { return false }
func (f *fakeInner) SupportsOffsetPaging() bool         { return false }

func (f *fakeInner) Grab(context.Context, string) (*search.GrabResult, error) {
	return nil, errors.New("not implemented")
}

func (f *fakeInner) Search(_ context.Context, _ search.Query) ([]*normalizer.Release, error) {
	atomic.AddInt64(&f.calls, 1)
	f.mu.Lock()
	gate := f.gate
	firstSeen := f.firstSeen
	f.mu.Unlock()
	if firstSeen != nil {
		f.firstOnce.Do(func() { close(firstSeen) })
	}
	if gate != nil {
		<-gate
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return nil, f.err
	}
	return f.releases, nil
}

func (f *fakeInner) callCount() int64 { return atomic.LoadInt64(&f.calls) }

// testCache builds a SearchCache over a migrated in-memory DB with an instance row,
// returning the cache, the instance id, and a settable clock pointer.
func testCache(t *testing.T, ttl ttlConfig, refreshPct int) (*SearchCache, int64, *atomic.Pointer[time.Time]) {
	t.Helper()
	db, err := database.Open(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	instID := insertTestInstance(t, db)

	var clk atomic.Pointer[time.Time]
	now := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	clk.Store(&now)
	sc := NewSearchCache(db, cacheTuning{enabled: true, ttl: ttl, refreshAt: refreshPct, cleanup: time.Hour}, func() time.Time { return *clk.Load() }, zerolog.Nop())
	return sc, instID, &clk
}

// insertTestInstance inserts a minimal enabled instance so cache rows satisfy the
// FK, returning its id.
func insertTestInstance(t *testing.T, db *database.DB) int64 {
	t.Helper()
	now := time.Now().UTC().Format("2006-01-02T15:04:05Z07:00")
	res, err := db.ExecContext(context.Background(),
		`INSERT INTO indexer_instances (slug, definition_id, name, base_url, enabled, created_at, updated_at)
		 VALUES (?, ?, ?, ?, 1, ?, ?)`,
		"fake", "fakedef", "Fake", "", now, now)
	if err != nil {
		t.Fatalf("insert instance: %v", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("last insert id: %v", err)
	}
	return id
}

func relSet(titles ...string) []*normalizer.Release {
	out := make([]*normalizer.Release, 0, len(titles))
	for _, t := range titles {
		out = append(out, &normalizer.Release{Title: t})
	}
	return out
}

var keywordTTL = ttlConfig{rss: 5 * time.Minute, keyword: 30 * time.Minute, thin: 2 * time.Minute, thinThreshold: 5}

// TestCacheHitDoesNotCallInner proves a second identical search is served from the
// cache without touching the wrapped indexer.
func TestCacheHitDoesNotCallInner(t *testing.T) {
	t.Parallel()
	sc, instID, _ := testCache(t, keywordTTL, 0)
	inner := &fakeInner{releases: relSet("Alpha", "Beta")}
	idx := sc.probe(inner, instID, nil)
	ctx := context.Background()
	q := search.Query{Keywords: "alpha"}

	first, err := idx.Search(ctx, q)
	if err != nil {
		t.Fatalf("first search: %v", err)
	}
	if len(first) != 2 {
		t.Fatalf("first len = %d, want 2", len(first))
	}
	// Allow the async store inside the miss path to complete (it is synchronous in
	// liveAndStore, so no wait needed) then second search.
	second, err := idx.Search(ctx, q)
	if err != nil {
		t.Fatalf("second search: %v", err)
	}
	if len(second) != 2 || second[0].Title != "Alpha" {
		t.Fatalf("second = %+v, want cached Alpha/Beta", second)
	}
	if got := inner.callCount(); got != 1 {
		t.Fatalf("inner called %d times, want 1 (second served from cache)", got)
	}
}

// TestConcurrentMissesSingleflight proves N concurrent identical misses drive the
// wrapped indexer exactly once.
func TestConcurrentMissesSingleflight(t *testing.T) {
	t.Parallel()
	sc, instID, _ := testCache(t, keywordTTL, 0)
	gate := make(chan struct{})
	inner := &fakeInner{releases: relSet("X"), gate: gate, firstSeen: make(chan struct{})}
	idx := sc.probe(inner, instID, nil)
	q := search.Query{Keywords: "x"}

	const n = 16
	var wg sync.WaitGroup
	wg.Add(n)
	errs := make([]error, n)
	for i := range n {
		go func(i int) {
			defer wg.Done()
			_, errs[i] = idx.Search(context.Background(), q)
		}(i)
	}
	// Wait deterministically until the first call has actually entered Search (the
	// flight is now in progress and holding the gate); the other callers coalesce onto
	// it. A latecomer that arrives after the gate opens still resolves to one inner
	// call via the in-flight double-check, so callCount stays 1 either way.
	<-inner.firstSeen
	close(gate)
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("search %d: %v", i, err)
		}
	}
	if got := inner.callCount(); got != 1 {
		t.Fatalf("inner called %d times under singleflight, want 1", got)
	}
}

// TestBypassForcesInnerAndWritesBack proves nocache forces a live search and still
// writes the result back so a subsequent cached read finds it.
func TestBypassForcesInnerAndWritesBack(t *testing.T) {
	t.Parallel()
	sc, instID, _ := testCache(t, keywordTTL, 0)
	inner := &fakeInner{releases: relSet("Fresh")}
	idx := sc.probe(inner, instID, nil)
	q := search.Query{Keywords: "fresh"}

	// Prime the cache with a normal miss.
	if _, err := idx.Search(context.Background(), q); err != nil {
		t.Fatalf("prime: %v", err)
	}
	// Bypass must call inner again despite the warm entry.
	bypassCtx := torznabhttp.WithCacheBypass(context.Background())
	if _, err := idx.Search(bypassCtx, q); err != nil {
		t.Fatalf("bypass search: %v", err)
	}
	if got := inner.callCount(); got != 2 {
		t.Fatalf("inner called %d times, want 2 (prime + bypass)", got)
	}
	// The bypass write-back kept the entry warm: a normal read serves cached.
	if _, err := idx.Search(context.Background(), q); err != nil {
		t.Fatalf("post-bypass read: %v", err)
	}
	if got := inner.callCount(); got != 2 {
		t.Fatalf("inner called %d times, want 2 (post-bypass read served from cache)", got)
	}
}

// TestStaleHitFiresOneRefresh proves a hit past the refresh-ahead threshold returns
// the cached value immediately (without blocking on inner) and triggers at most one
// background refresh that writes the fresh value back.
func TestStaleHitFiresOneRefresh(t *testing.T) {
	t.Parallel()
	sc, instID, clk := testCache(t, keywordTTL, 80)
	// Both result sets exceed thinThreshold (5) so the entry gets the full 30m
	// keyword TTL, not the 2m thin clamp — otherwise the entry would expire before
	// the refresh-ahead window and the stale read would be a plain miss.
	v1 := relSet("a1", "a2", "a3", "a4", "a5", "a6")
	v2 := relSet("b1", "b2", "b3", "b4", "b5", "b6")
	gate := make(chan struct{})
	inner := &fakeInner{releases: v1, gate: gate}
	idx := sc.probe(inner, instID, nil)
	q := search.Query{Keywords: "v"}

	// Prime: first miss stores v1 (open the gate so the prime does not block).
	close(gate)
	if _, err := idx.Search(context.Background(), q); err != nil {
		t.Fatalf("prime: %v", err)
	}
	if inner.callCount() != 1 {
		t.Fatalf("prime call count = %d, want 1", inner.callCount())
	}

	// Re-gate so the SWR refresh blocks, and swap the fresh result to v2.
	gate2 := make(chan struct{})
	inner.mu.Lock()
	inner.gate = gate2
	inner.releases = v2
	inner.mu.Unlock()

	// Advance the clock past 80% of the 30m keyword TTL (24m) but before expiry.
	advance(clk, 25*time.Minute)

	// Several stale hits while the refresh is gated: each serves the OLD value
	// immediately (never blocks) and all coalesce onto ONE in-flight refresh.
	for range 5 {
		got, err := idx.Search(context.Background(), q)
		if err != nil {
			t.Fatalf("stale hit: %v", err)
		}
		if len(got) != 6 || got[0].Title != "a1" {
			t.Fatalf("stale hit served %+v, want cached v1", got)
		}
	}
	// Exactly one refresh started (prime=1, refresh=1) despite five stale hits.
	waitForCall(t, inner, 2)
	if c := inner.callCount(); c != 2 {
		t.Fatalf("inner called %d times under refresh-ahead, want 2 (singleflight coalesces)", c)
	}

	// Release the gated refresh; its success-only write-back stores v2.
	close(gate2)
	// A subsequent read eventually serves the refreshed v2 once the write-back
	// commits. The fresh entry (cached now) is no longer past the refresh window,
	// so no further refresh fires.
	waitForTitle(t, idx, q, "b1")
}

// TestInnerErrorNotCached proves a live error propagates and is never cached: the
// next search retries inner.
func TestInnerErrorNotCached(t *testing.T) {
	t.Parallel()
	sc, instID, _ := testCache(t, keywordTTL, 0)
	sentinel := errors.New("boom")
	inner := &fakeInner{err: sentinel}
	idx := sc.probe(inner, instID, nil)
	q := search.Query{Keywords: "err"}

	if _, err := idx.Search(context.Background(), q); !errors.Is(err, sentinel) {
		t.Fatalf("first error = %v, want sentinel", err)
	}
	// Recover: clear the error so the retry succeeds — proving nothing was cached.
	inner.mu.Lock()
	inner.err = nil
	inner.releases = relSet("OK")
	inner.mu.Unlock()

	got, err := idx.Search(context.Background(), q)
	if err != nil {
		t.Fatalf("retry: %v", err)
	}
	if len(got) != 1 || got[0].Title != "OK" {
		t.Fatalf("retry = %+v, want OK (error was not cached)", got)
	}
	if c := inner.callCount(); c != 2 {
		t.Fatalf("inner called %d times, want 2 (error retry)", c)
	}
}

// TestStatsHitMissRatio proves the cumulative (restart-persistent) counters track hits and misses.
func TestStatsHitMissRatio(t *testing.T) {
	t.Parallel()
	sc, instID, _ := testCache(t, keywordTTL, 0)
	inner := &fakeInner{releases: relSet("A")}
	idx := sc.probe(inner, instID, nil)
	q := search.Query{Keywords: "a"}
	ctx := context.Background()

	if _, err := idx.Search(ctx, q); err != nil { // miss
		t.Fatalf("miss: %v", err)
	}
	if _, err := idx.Search(ctx, q); err != nil { // hit
		t.Fatalf("hit: %v", err)
	}
	stats, err := sc.Stats(ctx)
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	if stats.Hits != 1 || stats.Misses != 1 {
		t.Fatalf("hits/misses = %d/%d, want 1/1", stats.Hits, stats.Misses)
	}
	if stats.HitRatio != 0.5 {
		t.Fatalf("hit ratio = %v, want 0.5", stats.HitRatio)
	}
	if stats.Entries != 1 {
		t.Fatalf("entries = %d, want 1", stats.Entries)
	}
}

// TestCacheInfoRecordedOnMissAndHit proves the cache fills the request's CacheInfo
// sink with Cached=true + a stable expiry on both the storing miss and the serving
// hit, so the feed handler knows to emit conditional-GET validators.
func TestCacheInfoRecordedOnMissAndHit(t *testing.T) {
	t.Parallel()
	sc, instID, _ := testCache(t, keywordTTL, 0)
	inner := &fakeInner{releases: relSet("Alpha", "Beta")}
	idx := sc.probe(inner, instID, nil)
	q := search.Query{Keywords: "alpha"}

	// Miss: the store-back records the cache info for this request.
	missCtx, missInfo := torznabhttp.WithCacheInfoSink(context.Background())
	if _, err := idx.Search(missCtx, q); err != nil {
		t.Fatalf("miss: %v", err)
	}
	if !missInfo.Cached || missInfo.ExpiresAt.IsZero() {
		t.Fatalf("miss did not record cache info: %+v", missInfo)
	}

	// Hit: a fresh sink is filled with Cached=true too.
	hitCtx, hitInfo := torznabhttp.WithCacheInfoSink(context.Background())
	if _, err := idx.Search(hitCtx, q); err != nil {
		t.Fatalf("hit: %v", err)
	}
	if !hitInfo.Cached {
		t.Errorf("hit did not record Cached=true")
	}
	// The hit path must also record the entry's expiry (for the Cache-Control max-age),
	// and with the fixed test clock it matches the miss's stored expiry exactly.
	if hitInfo.ExpiresAt.IsZero() {
		t.Error("hit did not record an expiry")
	}
	if !hitInfo.ExpiresAt.Equal(missInfo.ExpiresAt) {
		t.Errorf("hit expiry %v != miss expiry %v", hitInfo.ExpiresAt, missInfo.ExpiresAt)
	}
	if inner.callCount() != 1 {
		t.Fatalf("inner called %d times, want 1 (second served from cache)", inner.callCount())
	}
}

// TestCacheInfoRecordedForCoalescedMisses proves every caller coalesced onto one
// singleflight miss gets the validators in its OWN sink (not just the flight leader),
// so a concurrent first-miss does not silently drop the ETag for the followers.
func TestCacheInfoRecordedForCoalescedMisses(t *testing.T) {
	t.Parallel()
	sc, instID, _ := testCache(t, keywordTTL, 0)
	gate := make(chan struct{})
	inner := &fakeInner{releases: relSet("X"), gate: gate, firstSeen: make(chan struct{})}
	idx := sc.probe(inner, instID, nil)
	q := search.Query{Keywords: "x"}

	const n = 8
	infos := make([]*torznabhttp.CacheInfo, n)
	var wg sync.WaitGroup
	wg.Add(n)
	for i := range n {
		ctx, ci := torznabhttp.WithCacheInfoSink(context.Background())
		infos[i] = ci
		go func() {
			defer wg.Done()
			_, _ = idx.Search(ctx, q)
		}()
	}
	<-inner.firstSeen // the flight is in progress; the rest coalesce onto it
	close(gate)
	wg.Wait()

	if inner.callCount() != 1 {
		t.Fatalf("inner called %d times, want 1 (coalesced)", inner.callCount())
	}
	for i, ci := range infos {
		if !ci.Cached || ci.ExpiresAt.IsZero() {
			t.Errorf("caller %d sink not filled: %+v", i, ci)
		}
	}
}

// advance moves the test clock forward by d.
func advance(clk *atomic.Pointer[time.Time], d time.Duration) {
	cur := *clk.Load()
	next := cur.Add(d)
	clk.Store(&next)
}

// waitForTitle polls idx.Search until the first served release has the wanted
// title (the SWR write-back is asynchronous) or a timeout fires.
func waitForTitle(t *testing.T, idx torznabhttp.Indexer, q search.Query, want string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	var lastTitle string
	for time.Now().Before(deadline) {
		got, err := idx.Search(context.Background(), q)
		if err != nil {
			t.Fatalf("waitForTitle search: %v", err)
		}
		if len(got) > 0 {
			lastTitle = got[0].Title
			if lastTitle == want {
				return
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("served title %q, never became %q", lastTitle, want)
}

// waitForCall blocks until inner's call count reaches want or a timeout fires.
func waitForCall(t *testing.T, inner *fakeInner, want int64) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if inner.callCount() >= want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("inner call count = %d, never reached %d", inner.callCount(), want)
}
