package registry_test

import (
	"context"
	"errors"
	"io"
	stdhttp "net/http"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/autobrr/harbrr/internal/database"
	"github.com/autobrr/harbrr/internal/domain"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
	"github.com/autobrr/harbrr/internal/indexer/native/catalog"
	"github.com/autobrr/harbrr/internal/indexer/registry"
)

// statusDoer returns a fixed status for every request (no network).
type statusDoer struct{ status int }

func (d statusDoer) Do(req *stdhttp.Request) (*stdhttp.Response, error) {
	return &stdhttp.Response{
		StatusCode: d.status,
		Header:     stdhttp.Header{},
		Body:       io.NopCloser(strings.NewReader("")),
		Request:    req,
	}, nil
}

// TestSearchRecordsHealthEvent proves a classified search failure is recorded as a
// health event and surfaced by Status: a 503 from the tracker -> rate_limited.
func TestSearchRecordsHealthEvent(t *testing.T) {
	t.Parallel()
	reg, _ := newRegistry(t, statusDoer{status: stdhttp.StatusServiceUnavailable})
	ctx := context.Background()
	if _, err := reg.Add(ctx, registry.AddParams{
		Slug: "tt", DefinitionID: "testtracker", Settings: map[string]string{"apikey": "x"},
	}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	idx, ok := reg.Indexer(ctx, "tt")
	if !ok {
		t.Fatal("Indexer(tt) not resolved")
	}
	if _, err := idx.Search(ctx, search.Query{Keywords: "bunny"}); !errors.Is(err, search.ErrRateLimited) {
		t.Fatalf("Search err = %v, want ErrRateLimited", err)
	}

	st, err := reg.Status(ctx, "tt")
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if st.Status != "failing" {
		t.Errorf("status = %q, want failing", st.Status)
	}
	if len(st.Events) != 1 || st.Events[0].Kind != domain.HealthRateLimited {
		t.Fatalf("events = %+v, want exactly one rate_limited", st.Events)
	}
}

// TestGrabRecordsHealthEvent proves a classified GRAB failure is health-recorded too
// (not just search): a 503 at grab time -> rate_limited, surfaced by Status. Before the
// fix, Grab never classified its errors, so this event was dropped.
func TestGrabRecordsHealthEvent(t *testing.T) {
	t.Parallel()
	reg, _ := newRegistry(t, statusDoer{status: stdhttp.StatusServiceUnavailable})
	ctx := context.Background()
	if _, err := reg.Add(ctx, registry.AddParams{
		Slug: "tt", DefinitionID: "testtracker", Settings: map[string]string{"apikey": "x"},
	}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	idx, ok := reg.Indexer(ctx, "tt")
	if !ok {
		t.Fatal("Indexer(tt) not resolved")
	}
	if _, err := idx.Grab(ctx, "https://testtracker.example/download/1"); !errors.Is(err, search.ErrRateLimited) {
		t.Fatalf("Grab err = %v, want ErrRateLimited", err)
	}

	st, err := reg.Status(ctx, "tt")
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if len(st.Events) != 1 || st.Events[0].Kind != domain.HealthRateLimited {
		t.Fatalf("events = %+v, want exactly one rate_limited from the grab", st.Events)
	}
}

// TestSuccessfulTestClearsCurrentHealth proves issue #116: a passing explicit Test
// immediately resolves a recent failure while preserving the append-only event and
// its cumulative failure count.
func TestSuccessfulTestClearsCurrentHealth(t *testing.T) {
	t.Parallel()
	reg, db := newRegistry(t, &replayDoer{body: bodyHTML})
	ctx := context.Background()
	inst, err := reg.Add(ctx, registry.AddParams{Slug: "tt", DefinitionID: "testtracker"})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := (database.Health{}).Record(ctx, db, domain.IndexerHealthEvent{
		InstanceID: inst.ID, Kind: domain.HealthParseError, Detail: "bad row", OccurredAt: fixedClock(),
	}); err != nil {
		t.Fatalf("record failure: %v", err)
	}

	before, err := reg.Status(ctx, "tt")
	if err != nil {
		t.Fatalf("Status before Test: %v", err)
	}
	if before.Status != "failing" {
		t.Fatalf("status before Test = %q, want failing", before.Status)
	}
	if err := reg.Test(ctx, "tt"); err != nil {
		t.Fatalf("Test: %v", err)
	}

	after, err := reg.Status(ctx, "tt")
	if err != nil {
		t.Fatalf("Status after Test: %v", err)
	}
	if after.Status != "healthy" {
		t.Errorf("status after Test = %q, want healthy", after.Status)
	}
	if len(after.Events) != 1 || after.Events[0].Kind != domain.HealthParseError {
		t.Errorf("events after Test = %+v, want preserved parse_error", after.Events)
	}
	stats, err := reg.Stats(ctx, "tt")
	if err != nil {
		t.Fatalf("Stats after Test: %v", err)
	}
	if stats.Failures.ParseError != 1 {
		t.Errorf("parse failure count after Test = %d, want 1", stats.Failures.ParseError)
	}
}

// TestSearchGatewayOutageRecordsTransportEvent proves the widened origin-down family
// (#457): every Cloudflare code that means "the origin never answered" — 521 web server
// down, 523 origin unreachable, 524 origin timed out, 530 origin DNS error — used to fall
// through as a plain "returned HTTP 5xx" and record NOTHING. Each now classifies as
// transport and derives failing.
func TestSearchGatewayOutageRecordsTransportEvent(t *testing.T) {
	t.Parallel()
	for _, status := range []int{521, 523, 524, 530} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			t.Parallel()
			reg, _ := newRegistry(t, statusDoer{status: status})
			ctx := context.Background()
			if _, err := reg.Add(ctx, registry.AddParams{
				Slug: "tt", DefinitionID: "testtracker", Settings: map[string]string{"apikey": "x"},
			}); err != nil {
				t.Fatalf("Add: %v", err)
			}
			idx, ok := reg.Indexer(ctx, "tt")
			if !ok {
				t.Fatal("Indexer(tt) not resolved")
			}
			if _, err := idx.Search(ctx, search.Query{Keywords: "bunny"}); !errors.Is(err, search.ErrGatewayStatus) {
				t.Fatalf("Search err = %v, want ErrGatewayStatus", err)
			}

			st, err := reg.Status(ctx, "tt")
			if err != nil {
				t.Fatalf("Status: %v", err)
			}
			if st.Status != registry.StatusFailing {
				t.Errorf("status = %q, want failing", st.Status)
			}
			if len(st.Events) != 1 || st.Events[0].Kind != domain.HealthTransport {
				t.Fatalf("events = %+v, want exactly one transport", st.Events)
			}
		})
	}
}

// TestFailingSearchesNeverDeriveHealthy is the #457 regression, sharpened by #484: an
// indexer whose every search fails with an UNCLASSIFIED error (a plain 500 — the tracker
// answered, so no health event is written and the breaker never trips) must not keep
// reading healthy off the failing traffic itself. #457 could only demote it to unknown
// once the success aged out; #484's "queried since the last success" rule calls it
// failing outright — immediately, with nothing classified and no window to wait out.
func TestFailingSearchesNeverDeriveHealthy(t *testing.T) {
	t.Parallel()
	doer := &swapDoer{body: bodyHTML}
	clk := &movableClock{t: fixedClock()}
	reg, _ := newClockedRegistry(t, doer, clk.now)
	ctx := context.Background()
	if _, err := reg.Add(ctx, registry.AddParams{
		Slug: "tt", DefinitionID: "testtracker", Settings: map[string]string{"apikey": "x"},
	}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	idx, ok := reg.Indexer(ctx, "tt")
	if !ok {
		t.Fatal("Indexer(tt) not resolved")
	}
	if _, err := idx.Search(ctx, search.Query{Keywords: "bunny"}); err != nil {
		t.Fatalf("Search (working): %v", err)
	}
	if st, err := reg.Status(ctx, "tt"); err != nil || st.Status != registry.StatusHealthy {
		t.Fatalf("status after a real success = %q (err %v), want healthy", st.Status, err)
	}

	// The tracker goes down in a shape nothing classifies, and the consumer keeps polling.
	doer.fail.Store(stdhttp.StatusInternalServerError)
	for range 3 {
		clk.advance(30 * time.Minute)
		if _, err := idx.Search(ctx, search.Query{Keywords: "bunny"}); err == nil {
			t.Fatal("Search (down) = nil error, want the tracker's 500")
		}
	}

	st, err := reg.Status(ctx, "tt")
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if len(st.Events) != 0 {
		t.Fatalf("events = %+v, want none (an unclassified failure records nothing)", st.Events)
	}
	if st.Status != registry.StatusFailing {
		t.Errorf("status after 90m of failing searches = %q, want failing", st.Status)
	}

	// And it self-heals on the next success, with nothing to wait out.
	doer.fail.Store(0)
	clk.advance(time.Minute)
	if _, err := idx.Search(ctx, search.Query{Keywords: "bunny"}); err != nil {
		t.Fatalf("Search (recovered): %v", err)
	}
	if st, err := reg.Status(ctx, "tt"); err != nil || st.Status != registry.StatusHealthy {
		t.Errorf("status after the tracker recovers = %q (err %v), want healthy", st.Status, err)
	}
}

// hangupDoer answers with body until cancel is set, after which it emulates the CONSUMER
// hanging up mid-search: it cancels the caller's context and fails the way net/http does,
// with a *url.Error carrying context.Canceled. Sequential use only (no locking).
type hangupDoer struct {
	body   string
	cancel context.CancelFunc
}

func (d *hangupDoer) Do(req *stdhttp.Request) (*stdhttp.Response, error) {
	if d.cancel != nil {
		d.cancel()
		return nil, &url.Error{Op: req.Method, URL: req.URL.String(), Err: context.Canceled}
	}
	return &stdhttp.Response{
		StatusCode: stdhttp.StatusOK,
		Header:     stdhttp.Header{},
		Body:       io.NopCloser(strings.NewReader(d.body)),
		Request:    req,
	}, nil
}

// TestCancelledSearchDoesNotMoveHealth pins the seam #484's derivation made dangerous: a
// Sonarr/qui HTTP timeout or a dropped connection aborts the search before anything is
// learned about the tracker, so it must move NOTHING — no query stamp (the sticky "queried
// since the last success" rule would otherwise read failing forever, with no reason to
// show) and no health event (a cancelled request surfaces as a *url.Error, which the
// transport classifier would otherwise happily record and escalate on). It is the same
// call the cache's breaker and the aggregate ledger already refuse to blame a tracker for.
func TestCancelledSearchDoesNotMoveHealth(t *testing.T) {
	t.Parallel()
	doer := &hangupDoer{body: bodyHTML}
	clk := &movableClock{t: fixedClock()}
	reg, _ := newClockedRegistry(t, doer, clk.now)
	ctx := context.Background()
	if _, err := reg.Add(ctx, registry.AddParams{
		Slug: "tt", DefinitionID: "testtracker", Settings: map[string]string{"apikey": "x"},
	}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	idx, ok := reg.Indexer(ctx, "tt")
	if !ok {
		t.Fatal("Indexer(tt) not resolved")
	}
	if _, err := idx.Search(ctx, search.Query{Keywords: "bunny"}); err != nil {
		t.Fatalf("Search (live): %v", err)
	}
	if st, err := reg.Status(ctx, "tt"); err != nil || st.Status != registry.StatusHealthy {
		t.Fatalf("status after a real success = %q (err %v), want healthy", st.Status, err)
	}

	// The consumer hangs up mid-search, a minute later (so a query stamp would land
	// strictly after the success and trip the sticky rule).
	clk.advance(time.Minute)
	cancelCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	doer.cancel = cancel
	if _, err := idx.Search(cancelCtx, search.Query{Keywords: "bunny"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("Search (cancelled) err = %v, want context.Canceled", err)
	}

	st, err := reg.Status(ctx, "tt")
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if st.Status != registry.StatusHealthy {
		t.Errorf("status after the consumer hung up = %q, want healthy (nothing reached the tracker)", st.Status)
	}
	if len(st.Events) != 0 {
		t.Errorf("events = %+v, want none (a cancelled consumer is not a tracker failure)", st.Events)
	}
}

// TestCachedSearchKeepsHealthy is #484's highest-risk regression: the "queried since the
// last success" rule reads a query counter, and a search served from the cache must not
// touch it. If it did, an indexer answering every poll from cache — the whole point of
// the cache — would flip to failing without a single thing going wrong. The counter is
// stamped in liveSearch, below the cache read, which is what makes this hold.
func TestCachedSearchKeepsHealthy(t *testing.T) {
	t.Parallel()
	db, ldr, keyring := newRegistryDeps(t)
	clk := &movableClock{t: fixedClock()}
	doer := &replayDoer{body: bodyHTML}
	reg := registry.New(db, ldr, keyring, catalog.All(),
		registry.WithClock(clk.now),
		registry.WithSearchCache(registry.NewSearchCacheForTest(db, clk.now)),
		registry.WithDoerFactory(func(registry.ClientParams) (search.Doer, error) { return doer, nil }))
	ctx := context.Background()
	if _, err := reg.Add(ctx, registry.AddParams{
		Slug: "tt", DefinitionID: "testtracker", Settings: map[string]string{"apikey": "x"},
	}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	idx, ok := reg.Indexer(ctx, "tt")
	if !ok {
		t.Fatal("Indexer(tt) not resolved")
	}
	if _, err := idx.Search(ctx, search.Query{Keywords: "bunny"}); err != nil {
		t.Fatalf("Search (live): %v", err)
	}
	served := len(doer.reqs)

	// Later — but inside the entry's TTL — the same query is served from the cache.
	clk.advance(time.Minute)
	if _, err := idx.Search(ctx, search.Query{Keywords: "bunny"}); err != nil {
		t.Fatalf("Search (cached): %v", err)
	}
	if got := len(doer.reqs); got != served {
		t.Fatalf("outbound requests = %d, want %d (the second search must be a cache hit)", got, served)
	}
	if st, err := reg.Status(ctx, "tt"); err != nil || st.Status != registry.StatusHealthy {
		t.Errorf("status after a cache-served search = %q (err %v), want healthy", st.Status, err)
	}
}

// TestLastSuccessSurvivesRestart proves the success signal is durable: a real success is
// flushed with the stat counters and rehydrated by a fresh registry over the same DB, so
// a restart does not blank the fleet to unknown.
func TestLastSuccessSurvivesRestart(t *testing.T) {
	t.Parallel()
	db, ldr, keyring := newRegistryDeps(t)
	clk := &movableClock{t: fixedClock()}
	newReg := func() *registry.Registry {
		return registry.New(db, ldr, keyring, catalog.All(),
			registry.WithClock(clk.now),
			registry.WithDoerFactory(func(registry.ClientParams) (search.Doer, error) {
				return &replayDoer{body: bodyHTML}, nil
			}))
	}
	reg := newReg()
	ctx := context.Background()
	if _, err := reg.Add(ctx, registry.AddParams{
		Slug: "tt", DefinitionID: "testtracker", Settings: map[string]string{"apikey": "x"},
	}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	idx, ok := reg.Indexer(ctx, "tt")
	if !ok {
		t.Fatal("Indexer(tt) not resolved")
	}
	if _, err := idx.Search(ctx, search.Query{Keywords: "bunny"}); err != nil {
		t.Fatalf("Search: %v", err)
	}
	reg.FlushStats(ctx)

	restarted := newReg()
	if err := restarted.RehydrateStats(ctx); err != nil {
		t.Fatalf("RehydrateStats: %v", err)
	}
	clk.advance(30 * time.Minute)
	st, err := restarted.Status(ctx, "tt")
	if err != nil {
		t.Fatalf("Status after restart: %v", err)
	}
	if st.Status != registry.StatusHealthy {
		t.Errorf("status after restart = %q, want healthy (rehydrated last success)", st.Status)
	}
}

// swapDoer serves a saved 200 body until fail holds a status code, then answers with
// that status instead — one indexer whose searches work and then stop.
type swapDoer struct {
	body string
	fail atomic.Int64
}

func (d *swapDoer) Do(req *stdhttp.Request) (*stdhttp.Response, error) {
	if code := d.fail.Load(); code != 0 {
		return statusDoer{status: int(code)}.Do(req)
	}
	return &stdhttp.Response{
		StatusCode: stdhttp.StatusOK,
		Header:     stdhttp.Header{},
		Body:       io.NopCloser(strings.NewReader(d.body)),
		Request:    req,
	}, nil
}

// newClockedRegistry is newRegistry with a caller-supplied clock, so a test can age an
// indexer's traffic and cache entries without sleeping.
func newClockedRegistry(t *testing.T, doer search.Doer, clock func() time.Time) (*registry.Registry, *database.DB) {
	t.Helper()
	db, ldr, keyring := newRegistryDeps(t)
	return registry.New(db, ldr, keyring, catalog.All(),
		registry.WithClock(clock),
		registry.WithDoerFactory(func(registry.ClientParams) (search.Doer, error) { return doer, nil })), db
}

// TestStatusUnknownSlug: Status for a missing indexer is ErrNotFound (404 at the API).
func TestStatusUnknownSlug(t *testing.T) {
	t.Parallel()
	reg, _ := newRegistry(t, statusDoer{status: stdhttp.StatusOK})
	if _, err := reg.Status(context.Background(), "nope"); !errors.Is(err, database.ErrNotFound) {
		t.Fatalf("err = %v, want database.ErrNotFound", err)
	}
}

// TestSlugsWithStatusAgreesWithAllStatuses proves the status selector (#389 PR 3) is
// the SAME derivation the roll-up serves, not a second copy that can drift: for a
// fleet holding all three states, each SlugsWithStatus answer is exactly the subset
// AllStatuses derived. It also pins ValidStatus to that vocabulary.
func TestSlugsWithStatusAgreesWithAllStatuses(t *testing.T) {
	t.Parallel()
	reg, db := newRegistry(t, statusDoer{status: stdhttp.StatusOK})
	ctx := context.Background()
	ids := map[string]int64{}
	for _, slug := range []string{"failing-one", "healthy-one", "unknown-one"} {
		inst, err := reg.Add(ctx, registry.AddParams{Slug: slug, DefinitionID: "testtracker"})
		if err != nil {
			t.Fatalf("Add %q: %v", slug, err)
		}
		ids[slug] = inst.ID
	}
	if err := (database.Health{}).Record(ctx, db, domain.IndexerHealthEvent{
		InstanceID: ids["failing-one"], Kind: domain.HealthAuthFailure, Detail: "login failed",
		OccurredAt: fixedClock(),
	}); err != nil {
		t.Fatalf("record failure: %v", err)
	}
	if err := (database.Health{}).RecordRecovery(ctx, db, ids["healthy-one"], fixedClock()); err != nil {
		t.Fatalf("record recovery: %v", err)
	}

	all, err := reg.AllStatuses(ctx)
	if err != nil {
		t.Fatalf("AllStatuses: %v", err)
	}
	for _, state := range []string{registry.StatusHealthy, registry.StatusFailing, registry.StatusUnknown} {
		if !registry.ValidStatus(state) {
			t.Errorf("ValidStatus(%q) = false, want true", state)
		}
		var want []string
		for _, st := range all {
			if st.Status == state {
				want = append(want, st.Slug)
			}
		}
		got, err := reg.SlugsWithStatus(ctx, state)
		if err != nil {
			t.Fatalf("SlugsWithStatus(%q): %v", state, err)
		}
		if len(got) != len(want) {
			t.Fatalf("SlugsWithStatus(%q) = %v, want %v", state, got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("SlugsWithStatus(%q)[%d] = %q, want %q", state, i, got[i], want[i])
			}
		}
	}
	if registry.ValidStatus("degraded") {
		t.Error(`ValidStatus("degraded") = true, want false`)
	}
}

// TestStatusReportsFailingSince proves the operator-visible streak start (#389 PR 3)
// is the circuit's InitialFailure, and that it is reported only while the indexer
// actually reads failing — a partially-recovered indexer keeps InitialFailure set, and
// claiming "failing since" for it would be a lie.
func TestStatusReportsFailingSince(t *testing.T) {
	t.Parallel()
	reg, db := newRegistry(t, statusDoer{status: stdhttp.StatusOK})
	ctx := context.Background()
	inst, err := reg.Add(ctx, registry.AddParams{Slug: "tt", DefinitionID: "testtracker"})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	started := fixedClock().Add(-3 * time.Hour)
	if err := (database.Circuit{}).Upsert(ctx, db, database.CircuitState{
		InstanceID: inst.ID, EscalationLevel: 2, InitialFailure: started,
		DisabledTill: fixedClock().Add(time.Hour),
	}); err != nil {
		t.Fatalf("upsert circuit: %v", err)
	}

	st, err := reg.Status(ctx, "tt")
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if st.Status != registry.StatusFailing {
		t.Fatalf("status = %q, want failing (circuit open)", st.Status)
	}
	if st.FailingSince == nil || !st.FailingSince.Equal(started) {
		t.Fatalf("failingSince = %v, want %v", st.FailingSince, started)
	}

	// Ladder still partly climbed (InitialFailure survives the first rung down) but the
	// disable window has closed and a passing test proves it works: the failing-since
	// instant must go away with the state it describes.
	if err := (database.Circuit{}).Upsert(ctx, db, database.CircuitState{
		InstanceID: inst.ID, EscalationLevel: 1, InitialFailure: started,
	}); err != nil {
		t.Fatalf("upsert recovered circuit: %v", err)
	}
	if err := (database.Health{}).RecordRecovery(ctx, db, inst.ID, fixedClock()); err != nil {
		t.Fatalf("record recovery: %v", err)
	}
	after, err := reg.Status(ctx, "tt")
	if err != nil {
		t.Fatalf("Status after recovery: %v", err)
	}
	if after.Status != registry.StatusHealthy {
		t.Fatalf("status after recovery = %q, want healthy", after.Status)
	}
	if after.FailingSince != nil {
		t.Errorf("failingSince after recovery = %v, want nil", after.FailingSince)
	}
}
