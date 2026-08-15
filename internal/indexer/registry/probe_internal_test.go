package registry

import (
	"context"
	"errors"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/autobrr/harbrr/internal/database"
	"github.com/autobrr/harbrr/internal/database/dbtest"
)

// newTestQueue builds a ProbeQueue over injected seams (no registry, no network) and
// runs it until the test ends. It returns the queue and a stop func that cancels and
// JOINS Run, so a test can assert on what happens after shutdown.
func newTestQueue(t *testing.T, probe func(context.Context, string) error, targets func(context.Context) ([]string, error)) (*ProbeQueue, func()) {
	t.Helper()
	q := &ProbeQueue{
		probe:   probe,
		targets: targets,
		log:     zerolog.Nop(),
		jobs:    make(chan probeJob, probeQueueDepth),
		seed:    make(chan struct{}, 1),
		pending: make(map[string]int),
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		defer close(done)
		q.Run(ctx)
	}()
	var once sync.Once
	stop := func() {
		once.Do(func() {
			cancel()
			<-done
		})
	}
	t.Cleanup(stop)
	return q, stop
}

// recorder collects the slugs a queue probed, in completion order.
type recorder struct {
	mu    sync.Mutex
	slugs []string
	done  chan string
}

func newRecorder(capacity int) *recorder {
	return &recorder{done: make(chan string, capacity)}
}

func (r *recorder) probe(_ context.Context, slug string) error {
	r.mu.Lock()
	r.slugs = append(r.slugs, slug)
	r.mu.Unlock()
	r.done <- slug
	return nil
}

// await drains n completions, failing the test rather than hanging forever if the
// queue never gets there. t.Context's deadline is the backstop; the timer here just
// gives a legible failure.
func (r *recorder) await(t *testing.T, n int) []string {
	t.Helper()
	got := make([]string, 0, n)
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	for range n {
		select {
		case slug := <-r.done:
			got = append(got, slug)
		case <-deadline.C:
			t.Fatalf("timed out waiting for %d probes; got %v", n, got)
		}
	}
	return got
}

// TestProbeQueueRespectsConcurrencyCap asserts the cap is enforced rather than assumed:
// every probe parks until released, so the number in flight is directly observable. The
// queue must reach exactly probeConcurrency and never exceed it, however many jobs are
// waiting.
func TestProbeQueueRespectsConcurrencyCap(t *testing.T) {
	t.Parallel()
	const jobs = probeConcurrency * 3

	var (
		inFlight atomic.Int64
		peak     atomic.Int64
	)
	started := make(chan struct{}, jobs)
	release := make(chan struct{})
	finished := make(chan struct{}, jobs)

	q, _ := newTestQueue(t, func(_ context.Context, _ string) error {
		now := inFlight.Add(1)
		for {
			was := peak.Load()
			if now <= was || peak.CompareAndSwap(was, now) {
				break
			}
		}
		started <- struct{}{}
		<-release
		inFlight.Add(-1)
		finished <- struct{}{}
		return nil
	}, nil)

	for i := range jobs {
		q.Enqueue(slugFor(i))
	}

	// Wait for a full batch of workers to be parked. A ninth cannot have started: it
	// would have to have taken a slot from a probe that is still blocked on release.
	for range probeConcurrency {
		<-started
	}
	if got := inFlight.Load(); got != probeConcurrency {
		t.Fatalf("in flight after a full batch = %d, want %d", got, probeConcurrency)
	}
	if got := len(started); got != 0 {
		t.Fatalf("%d extra probes started past the cap of %d", got, probeConcurrency)
	}

	close(release)
	for range jobs {
		<-finished
	}
	if got := peak.Load(); got != probeConcurrency {
		t.Fatalf("peak concurrency = %d, want exactly %d", got, probeConcurrency)
	}
}

// slugFor names the i-th synthetic probe target.
func slugFor(i int) string { return "ix-" + string(rune('a'+i%26)) }

// TestProbeQueueShutdownCancelsAndJoins proves Run does not return until the probe it
// started has observed cancellation and finished, and that nothing runs afterwards —
// the property that keeps a health write from landing after the database is closed.
func TestProbeQueueShutdownCancelsAndJoins(t *testing.T) {
	t.Parallel()

	entered := make(chan struct{})
	var (
		sawCancel atomic.Bool
		finished  atomic.Bool
		ran       atomic.Int64
	)
	q, stop := newTestQueue(t, func(ctx context.Context, _ string) error {
		ran.Add(1)
		close(entered)
		<-ctx.Done()
		sawCancel.Store(true)
		finished.Store(true)
		return ctx.Err()
	}, nil)

	q.Enqueue("blocking")
	<-entered

	stop() // cancels and joins Run

	if !sawCancel.Load() {
		t.Error("the in-flight probe never observed cancellation")
	}
	if !finished.Load() {
		t.Error("Run returned before the in-flight probe finished")
	}
	// Anything enqueued after shutdown is dropped, never run.
	q.Enqueue("after-shutdown")
	if got := ran.Load(); got != 1 {
		t.Fatalf("probes run = %d, want 1 (nothing may run after shutdown)", got)
	}
}

// TestProbeQueueProbing pins the signal the save flow reads: a slug is "probing" from
// the moment it is queued until its probe's health write has committed, and not a moment
// longer. Without it, an edit whose probe has not started yet looks exactly like one whose
// probe passed and left the indexer as healthy as it already was.
func TestProbeQueueProbing(t *testing.T) {
	t.Parallel()

	entered := make(chan struct{})
	release := make(chan struct{})
	finished := make(chan struct{})
	q, _ := newTestQueue(t, func(context.Context, string) error {
		close(entered)
		<-release
		return nil
	}, nil)

	if q.Probing("ix") {
		t.Fatal("Probing before anything was queued")
	}
	q.Enqueue("ix")
	<-entered
	if !q.Probing("ix") {
		t.Fatal("Probing = false while the probe is running")
	}
	if q.Probing("other") {
		t.Fatal("Probing = true for a slug nothing was queued for")
	}

	close(release)
	go func() {
		for q.Probing("ix") {
			runtime.Gosched()
		}
		close(finished)
	}()
	select {
	case <-finished:
	case <-time.After(10 * time.Second):
		t.Fatal("Probing stayed true after the probe returned")
	}
}

// TestProbeQueueSeedProbesTargets covers the boot trigger: Seed enumerates through the
// injected targets seam and probes each slug, without the caller waiting on either the
// enumeration or the probes.
func TestProbeQueueSeedProbesTargets(t *testing.T) {
	t.Parallel()

	want := []string{"alpha", "beta", "gamma"}
	rec := newRecorder(len(want))
	q, _ := newTestQueue(t, rec.probe, func(context.Context) ([]string, error) {
		return want, nil
	})

	q.Seed()

	got := rec.await(t, len(want))
	assertSameSlugs(t, got, want)
}

// TestProbeQueueSeedSurvivesTargetFailure pins the boot path's failure mode: a target
// enumeration error is logged and dropped, never a panic and never a partial probe run.
func TestProbeQueueSeedSurvivesTargetFailure(t *testing.T) {
	t.Parallel()

	var probed atomic.Int64
	q, stop := newTestQueue(t, func(context.Context, string) error {
		probed.Add(1)
		return nil
	}, func(context.Context) ([]string, error) {
		return nil, errors.New("db is down")
	})

	q.Seed()
	stop() // joining Run is what proves the seed goroutine completed

	if got := probed.Load(); got != 0 {
		t.Fatalf("probes run = %d, want 0 when enumeration failed", got)
	}
}

// TestProbeQueueTestAllReportsEveryIndexer covers the batch path (#485): each indexer
// gets its own verdict, a failing one does not abort the batch, and the results come
// back in the caller's order regardless of completion order.
func TestProbeQueueTestAllReportsEveryIndexer(t *testing.T) {
	t.Parallel()

	boom := errors.New("bad passkey")
	q, _ := newTestQueue(t, func(_ context.Context, slug string) error {
		if slug == "broken" {
			return boom
		}
		return nil
	}, nil)

	slugs := []string{"good", "broken", "also-good"}
	results, err := q.TestAll(t.Context(), slugs)
	if err != nil {
		t.Fatalf("TestAll: %v", err)
	}
	if len(results) != len(slugs) {
		t.Fatalf("results = %d, want %d", len(results), len(slugs))
	}
	for i, want := range slugs {
		if results[i].Slug != want {
			t.Fatalf("results[%d].Slug = %q, want %q", i, results[i].Slug, want)
		}
	}
	if results[0].Err != nil || results[2].Err != nil {
		t.Errorf("a passing indexer reported an error: %v / %v", results[0].Err, results[2].Err)
	}
	if !errors.Is(results[1].Err, boom) {
		t.Errorf("results[1].Err = %v, want %v", results[1].Err, boom)
	}
}

// TestProbeQueueTestAllRespectsConcurrencyCap proves the batch shares the SAME ceiling
// as every other trigger — the whole reason Test all moved off the browser's unbounded
// per-indexer fan-out.
func TestProbeQueueTestAllRespectsConcurrencyCap(t *testing.T) {
	t.Parallel()
	const jobs = probeConcurrency * 2

	var (
		inFlight atomic.Int64
		peak     atomic.Int64
	)
	started := make(chan struct{}, jobs)
	release := make(chan struct{})
	// Every probe parks until released, so the batch cannot drain past the cap while
	// the assertion below runs — the same directly-observable setup the fire-and-forget
	// cap test uses, driven through TestAll instead of Enqueue.
	q, _ := newTestQueue(t, func(context.Context, string) error {
		now := inFlight.Add(1)
		defer inFlight.Add(-1)
		for {
			was := peak.Load()
			if now <= was || peak.CompareAndSwap(was, now) {
				break
			}
		}
		started <- struct{}{}
		<-release
		return nil
	}, nil)

	slugs := make([]string, jobs)
	for i := range slugs {
		slugs[i] = slugFor(i)
	}
	batch := make(chan []ProbeResult, 1)
	go func() {
		results, err := q.TestAll(t.Context(), slugs)
		if err != nil {
			t.Errorf("TestAll: %v", err)
		}
		batch <- results
	}()

	for range probeConcurrency {
		<-started
	}
	if got := len(started); got != 0 {
		t.Errorf("%d extra probes started past the cap of %d", got, probeConcurrency)
	}
	close(release)

	results := <-batch
	if len(results) != jobs {
		t.Fatalf("results = %d, want %d", len(results), jobs)
	}
	if got := peak.Load(); got != probeConcurrency {
		t.Fatalf("peak concurrency = %d, want exactly %d", got, probeConcurrency)
	}
}

// TestProbeQueueTestAllAbandonedMidRun covers the navigate-away case: cancelling the
// caller's context stops the WAIT and reports the cancellation, but the probes already
// queued still finish — they are the queue's work, not the request's.
func TestProbeQueueTestAllAbandonedMidRun(t *testing.T) {
	t.Parallel()

	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	finished := make(chan struct{})
	q, _ := newTestQueue(t, func(context.Context, string) error {
		entered <- struct{}{}
		<-release
		close(finished)
		return nil
	}, nil)

	ctx, cancel := context.WithCancel(t.Context())
	batch := make(chan error, 1)
	go func() {
		_, err := q.TestAll(ctx, []string{"slow"})
		batch <- err
	}()

	<-entered
	cancel()
	if err := <-batch; !errors.Is(err, context.Canceled) {
		t.Fatalf("TestAll error = %v, want context.Canceled", err)
	}

	// The abandoned probe is still running, and still completes.
	close(release)
	<-finished
}

// TestProbeTargetsSelectsEveryUnhealthyIndexer pins the boot work list against real
// derived health: everything enabled that is not healthy is probed — an indexer with no
// evidence (unknown), one the circuit breaker is holding open, and one that is failing
// on rule 3 alone (queried, never succeeded: no event, no breaker window, nothing to
// expire). That last one is the trapdoor the old unknown-or-breaker-open selection left
// open: statusMembers keeps a failing indexer out of the status:healthy feed, so no
// traffic can ever produce the success that would clear it, and no probe went near it
// either. A disabled indexer is still skipped whatever its health.
func TestProbeTargetsSelectsEveryUnhealthyIndexer(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	db := dbtest.OpenMigrated(t)
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)

	seedProbeInstance(t, db, "unknown-ix", true)
	brokenID := seedProbeInstance(t, db, "breaker-ix", true)
	rule3ID := seedProbeInstance(t, db, "rule3-ix", true)
	seedProbeInstance(t, db, "disabled-ix", false)
	healthyID := seedProbeInstance(t, db, "healthy-ix", true)

	// A breaker-open indexer: disabled_till in the future derives failing.
	if err := (database.Circuit{}).Upsert(ctx, db, database.CircuitState{
		InstanceID: brokenID, EscalationLevel: 2, InitialFailure: now.Add(-time.Hour), DisabledTill: now.Add(time.Hour),
	}); err != nil {
		t.Fatalf("upsert circuit: %v", err)
	}
	// A recently-tested indexer derives healthy and needs no probe.
	if err := (database.Health{}).RecordRecovery(ctx, db, healthyID, now.Add(-time.Minute)); err != nil {
		t.Fatalf("record recovery: %v", err)
	}

	reg := New(db, nil, nil, nil, WithClock(func() time.Time { return now }))
	// A query that reached the tracker with no success after it — rule 3's failing,
	// carrying no event, no failing-since and no disable window.
	reg.StatsReporter.stats.RecordQuery(rule3ID, time.Second)
	if st, err := reg.Status(ctx, "rule3-ix"); err != nil || st.Status != StatusFailing || len(st.Events) != 0 {
		t.Fatalf("rule3-ix status = %q with %d events (err %v), want failing with none", st.Status, len(st.Events), err)
	}

	got, err := reg.ProbeTargets(ctx)
	if err != nil {
		t.Fatalf("ProbeTargets: %v", err)
	}
	assertSameSlugs(t, got, []string{"breaker-ix", "rule3-ix", "unknown-ix"})
}

// seedProbeInstance inserts a bare instance row with the given enabled flag.
func seedProbeInstance(t *testing.T, db *database.DB, slug string, enabled bool) int64 {
	t.Helper()
	stamp := "2026-06-01T00:00:00Z"
	res, err := db.ExecContext(t.Context(),
		`INSERT INTO indexer_instances (slug, definition_id, name, base_url, enabled, created_at, updated_at)
		 VALUES (?, ?, ?, '', ?, ?, ?)`,
		slug, "fakedef", slug, enabled, stamp, stamp)
	if err != nil {
		t.Fatalf("insert %q: %v", slug, err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("last insert id: %v", err)
	}
	return id
}

// assertSameSlugs compares two slug sets ignoring order.
func assertSameSlugs(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("slugs = %v, want %v", got, want)
	}
	seen := make(map[string]int, len(got))
	for _, s := range got {
		seen[s]++
	}
	for _, s := range want {
		seen[s]--
	}
	for s, n := range seen {
		if n != 0 {
			t.Fatalf("slugs = %v, want %v (mismatch on %q)", got, want, s)
		}
	}
}
