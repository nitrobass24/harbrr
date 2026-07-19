package registry

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/autobrr/harbrr/internal/database"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/mapper"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/normalizer"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
	"github.com/autobrr/harbrr/internal/indexer/core"
	"github.com/autobrr/harbrr/internal/indexer/native"
)

// budgetFakeDriver is a native.Driver test double whose Search/Grab behavior and
// error are swappable at runtime (unlike freeleech_internal_test.go's fakeDriver),
// and that counts calls — so a test can prove the tracker was (or was NOT) actually
// hit once the budget is exhausted.
type budgetFakeDriver struct {
	releases    []*normalizer.Release
	searchErr   error
	grabErr     error
	searchCalls atomic.Int64
	grabCalls   atomic.Int64
}

func (d *budgetFakeDriver) Capabilities() *mapper.Capabilities { return &mapper.Capabilities{} }
func (d *budgetFakeDriver) NeedsResolver() bool                { return false }
func (d *budgetFakeDriver) DownloadNeedsAuth() bool            { return false }
func (d *budgetFakeDriver) SupportsOffsetPaging() bool         { return false }
func (d *budgetFakeDriver) Test(context.Context) error         { return nil }

func (d *budgetFakeDriver) Search(context.Context, search.Query) ([]*normalizer.Release, error) {
	d.searchCalls.Add(1)
	if d.searchErr != nil {
		return nil, d.searchErr
	}
	return d.releases, nil
}

func (d *budgetFakeDriver) Grab(context.Context, string) (*search.GrabResult, error) {
	d.grabCalls.Add(1)
	if d.grabErr != nil {
		return nil, d.grabErr
	}
	return &search.GrabResult{}, nil
}

var _ native.Driver = (*budgetFakeDriver)(nil)

// newBudgetTestAdapter builds a real indexerAdapter over a migrated in-memory DB with
// a caching-enabled SearchCache and a RequestBudget sharing that same DB (so the
// instance FK and any persisted counters are consistent) — exercising the actual
// enforcement seam (indexerAdapter.Search/Grab) rather than the cache-aside probe.
func newBudgetTestAdapter(t *testing.T, inner *budgetFakeDriver, cfg map[string]string) (*indexerAdapter, *atomic.Pointer[time.Time]) {
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
	now := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)
	clk.Store(&now)
	clock := func() time.Time { return *clk.Load() }

	sc := newSearchCache(db, cacheTuning{enabled: true, ttl: ttlConfig{rss: time.Hour, keyword: time.Hour, thin: time.Hour}, cleanup: time.Hour}, clock, zerolog.Nop())
	budget := newRequestBudget(db, clock, zerolog.Nop())

	a := &indexerAdapter{
		info:         core.IndexerInfo{ID: "fake"},
		inner:        inner,
		instanceID:   instID,
		cfg:          cfg,
		cache:        sc,
		db:           db,
		health:       database.Health{},
		stats:        newIndexerStats(db, clock, zerolog.Nop()),
		budget:       budget,
		circuit:      database.Circuit{},
		circuitLocks: &circuitLocks{},
		startedAt:    now,
		clock:        clock,
		log:          zerolog.Nop(),
	}
	return a, &clk
}

// TestAdapterSearch_ExhaustedQueryServesStale proves the query-path half of #251's
// enforcement: once the configured query budget is spent, Search serves the last
// cached (even now-exhausted-because-past-TTL) entry instead of hitting the tracker
// again or refusing outright.
func TestAdapterSearch_ExhaustedQueryServesStale(t *testing.T) {
	t.Parallel()
	inner := &budgetFakeDriver{releases: []*normalizer.Release{{Title: "cached-release"}}}
	a, clk := newBudgetTestAdapter(t, inner, map[string]string{"query_limit": "1"})
	q := search.Query{Keywords: "x"}

	// First search: within budget, drives the tracker once and populates the cache.
	rels, err := a.Search(context.Background(), q)
	if err != nil {
		t.Fatalf("first Search: %v", err)
	}
	if len(rels) != 1 || rels[0].Title != "cached-release" {
		t.Fatalf("first Search releases = %+v", rels)
	}
	if got := inner.searchCalls.Load(); got != 1 {
		t.Fatalf("tracker hit %d times after first search, want 1", got)
	}

	// Advance well past the cache TTL so a normal cache lookup would MISS — the only
	// way a second Search can still avoid hitting the tracker is the budget's
	// prefer-stale path (fetchStale bypasses expiry).
	future := clk.Load().Add(2 * time.Hour)
	clk.Store(&future)

	rels, err = a.Search(context.Background(), q)
	if err != nil {
		t.Fatalf("second (budget-exhausted) Search: %v", err)
	}
	if len(rels) != 1 || rels[0].Title != "cached-release" {
		t.Fatalf("second Search releases = %+v, want the stale cached entry", rels)
	}
	if got := inner.searchCalls.Load(); got != 1 {
		t.Fatalf("tracker hit %d times after second search, want still 1 (never re-hit once budget exhausted)", got)
	}
}

// TestAdapterSearch_ExhaustedQueryBypassRefusesStale proves a nocache (cache-bypass)
// request never gets the prefer-stale serve: the caller explicitly opted out of
// cached results, so an exhausted budget surfaces the error instead of an expired
// cache entry.
func TestAdapterSearch_ExhaustedQueryBypassRefusesStale(t *testing.T) {
	t.Parallel()
	inner := &budgetFakeDriver{releases: []*normalizer.Release{{Title: "cached-release"}}}
	a, _ := newBudgetTestAdapter(t, inner, map[string]string{"query_limit": "1"})
	q := search.Query{Keywords: "x"}

	// Populate the cache and spend the whole budget.
	if _, err := a.Search(context.Background(), q); err != nil {
		t.Fatalf("first Search: %v", err)
	}

	ctx := core.WithCacheBypass(context.Background())
	if _, err := a.Search(ctx, q); !errors.Is(err, errBudgetExhausted) {
		t.Fatalf("bypass Search err = %v, want errBudgetExhausted (never a stale serve)", err)
	}
	if got := inner.searchCalls.Load(); got != 1 {
		t.Fatalf("tracker hit %d times, want 1 (bypass must not buy an outbound hit past the budget)", got)
	}
}

// TestAdapterSearch_ExhaustedQueryNoCacheRefuses proves that with nothing ever
// cached, an exhausted query budget surfaces the budget-exhausted error rather than
// silently returning an empty result or hitting the tracker.
func TestAdapterSearch_ExhaustedQueryNoCacheRefuses(t *testing.T) {
	t.Parallel()
	inner := &budgetFakeDriver{releases: nil}
	a, _ := newBudgetTestAdapter(t, inner, map[string]string{"query_limit": "0"})
	// query_limit=0 is treated as "invalid/non-positive -> no cap" by parseLimit, so
	// use MarkQuotaSpent directly to force an exhausted-with-nothing-cached state
	// without relying on a configured cap.
	a.budget.MarkQuotaSpent(context.Background(), a.instanceID, a.cfg, budgetKindQuery, a.clock())

	if _, err := a.Search(context.Background(), search.Query{Keywords: "x"}); !errors.Is(err, errBudgetExhausted) {
		t.Fatalf("err = %v, want errBudgetExhausted", err)
	}
	if got := inner.searchCalls.Load(); got != 0 {
		t.Fatalf("tracker hit %d times, want 0 (budget exhausted before any outbound hit)", got)
	}
}

// TestAdapterGrab_ExhaustedRefuses proves the grab-path half of #251's enforcement:
// grabs are never cached, so an exhausted grab budget refuses outright.
func TestAdapterGrab_ExhaustedRefuses(t *testing.T) {
	t.Parallel()
	inner := &budgetFakeDriver{}
	a, _ := newBudgetTestAdapter(t, inner, map[string]string{"grab_limit": "1"})

	if _, err := a.Grab(context.Background(), "https://tracker.example/dl"); err != nil {
		t.Fatalf("first Grab: %v", err)
	}
	if _, err := a.Grab(context.Background(), "https://tracker.example/dl"); !errors.Is(err, errBudgetExhausted) {
		t.Fatalf("second Grab err = %v, want errBudgetExhausted", err)
	}
	if got := inner.grabCalls.Load(); got != 1 {
		t.Fatalf("tracker grabbed %d times, want 1 (second refused before reaching the driver)", got)
	}
}

// TestAdapterSearch_QuotaErrorLearnsSpent proves the reactive-learning path end to
// end: a tracker error that unwraps to search.ErrQuotaExceeded (as newznab's code 910
// classification does) marks the query budget spent, so the VERY NEXT search — even
// with no operator-configured limit at all — never reaches the tracker again this
// period.
func TestAdapterSearch_QuotaErrorLearnsSpent(t *testing.T) {
	t.Parallel()
	quotaErr := &search.QuotaExceededError{Detail: "newznab: api error (code 910): Daily API limit reached"}
	inner := &budgetFakeDriver{searchErr: quotaErr}
	a, _ := newBudgetTestAdapter(t, inner, nil) // no configured limit at all

	if _, err := a.Search(context.Background(), search.Query{Keywords: "x"}); !errors.Is(err, search.ErrQuotaExceeded) {
		t.Fatalf("first Search err = %v, want it to unwrap to ErrQuotaExceeded", err)
	}
	if got := inner.searchCalls.Load(); got != 1 {
		t.Fatalf("tracker hit %d times, want 1 (the call that surfaced the quota error)", got)
	}

	// The reactive-learning latch should now refuse further queries THIS period,
	// without a second live hit — nothing was cached (the first call errored), so this
	// surfaces errBudgetExhausted rather than serving stale.
	inner.searchErr = nil // if the guard failed, the tracker would now happily answer
	if _, err := a.Search(context.Background(), search.Query{Keywords: "x"}); !errors.Is(err, errBudgetExhausted) {
		t.Fatalf("second Search err = %v, want errBudgetExhausted (learned quota-spent)", err)
	}
	if got := inner.searchCalls.Load(); got != 1 {
		t.Fatalf("tracker hit %d times after the learned mark, want still 1", got)
	}
}

// TestAdapterSearch_BudgetExhaustionDoesNotTripBreaker proves budget-exhaustion
// composes cleanly with the negative-result circuit breaker (#251 item 5): once the
// budget has ALREADY been marked spent (so Search refuses via errBudgetExhausted
// without the tracker ever answering), that refusal must NOT open the breaker for
// other consumers — a self-imposed guard is not a tracker failure worth suppressing
// everyone else over. (A genuine tracker error observed while reserving still trips
// the breaker as before — that IS a real failure, and is exactly how the reactive
// quota-learning path in TestAdapterSearch_QuotaErrorLearnsSpent gets to run at all.)
func TestAdapterSearch_BudgetExhaustionDoesNotTripBreaker(t *testing.T) {
	t.Parallel()
	inner := &budgetFakeDriver{}
	a, _ := newBudgetTestAdapter(t, inner, nil)
	a.cache.tuning.Load().ttl.negative = time.Minute // arm the breaker
	a.budget.MarkQuotaSpent(context.Background(), a.instanceID, a.cfg, budgetKindQuery, a.clock())

	if _, err := a.Search(context.Background(), search.Query{Keywords: "x"}); !errors.Is(err, errBudgetExhausted) {
		t.Fatalf("err = %v, want errBudgetExhausted", err)
	}
	if got := inner.searchCalls.Load(); got != 0 {
		t.Fatalf("tracker hit %d times, want 0", got)
	}
	if until := a.cache.breaker.openUntil(a.instanceID, a.clock()); !until.IsZero() {
		t.Fatalf("breaker openUntil = %v, want zero (a budget refusal must never trip the breaker)", until)
	}
}
