package registry

import (
	"context"
	"errors"
	"fmt"

	"github.com/rs/zerolog"
	"golang.org/x/sync/errgroup"

	apphttp "github.com/autobrr/harbrr/internal/http"
)

// probeConcurrency bounds how many credential probes run at once. It mirrors
// core.fanoutLimit (internal/indexer/core/aggregate.go) — a package-private constant
// there, so this is a deliberate copy rather than a shared symbol — and it is the same
// number for the same reason: per-host pacing, the request budget and the circuit
// breaker all live INSIDE each probe, and two indexers are two different hosts, so
// this is only a goroutine/fd ceiling, never the tracker-load control. What it does buy
// is the shared bottleneck several indexers may sit behind (one proxy, one FlareSolverr).
const probeConcurrency = 8

// probeQueueDepth is how many probes may be waiting for a worker. A boot seed enqueues
// one job per unhealthy enabled indexer and Test all one per configured indexer, so this
// is roughly an order of magnitude above the fleet size a single-user instance runs —
// deep enough that the drop path below is unreachable in practice, shallow enough that a
// runaway caller cannot pin unbounded memory.
const probeQueueDepth = 256

// ErrProbeQueueFull is the verdict a batch entry gets when the queue had no room for it.
// It is reported per indexer rather than failing the batch: the probes that did get a
// slot still run and still record their health.
var ErrProbeQueueFull = errors.New("registry: probe queue is full")

// probeJob is one queued credential probe.
type probeJob struct {
	slug string
	// done, when non-nil, receives the probe's verdict (nil means it passed). Only the
	// batch path (TestAll) sets it; the boot/create/credential-change triggers are
	// fire-and-forget and leave it nil.
	done func(error)
}

// ProbeQueue is the ONE bounded worker pool every automatic credential check runs
// through: the boot reconciliation of every indexer that is not healthy, the probe after an
// indexer is created, the probe after its credentials change, and the operator's
// "Test all" (autobrr/harbrr#484, #485). Having one queue — rather than one mechanism
// per trigger — is what makes probeConcurrency an instance-wide ceiling instead of a
// per-caller one.
//
// It probes with Test, not an empty Search: Test verifies the credentials, whereas a
// Search returning zero results is indistinguishable from one that simply matched
// nothing. A passing Test writes the recovery record the status derivation reads, so a
// probe that passes resolves the indexer's health.
//
// The queue holds no context: Run owns the lifetime, and everything it starts is joined
// before Run returns, so no probe — and no health write — outlives the app.
type ProbeQueue struct {
	probe   func(ctx context.Context, slug string) error
	targets func(ctx context.Context) ([]string, error)
	log     zerolog.Logger
	jobs    chan probeJob
	seed    chan struct{}
}

// NewProbeQueue builds the queue over reg's Test and boot-target seams. It does not
// start anything — the app lifecycle calls Run.
func NewProbeQueue(reg *Registry, log zerolog.Logger) *ProbeQueue {
	return &ProbeQueue{
		probe:   reg.Test,
		targets: reg.ProbeTargets,
		log:     log,
		jobs:    make(chan probeJob, probeQueueDepth),
		seed:    make(chan struct{}, 1),
	}
}

// Run drains the queue until ctx is cancelled, then waits for the probes still in
// flight and returns. Run is the queue's whole lifetime: nothing it starts can outlive
// it, so the app's join-then-close-the-database ordering holds for probe health writes
// exactly as it does for the reapers.
//
// errgroup.Go blocks once probeConcurrency probes are running, which is the bound —
// while it blocks, ctx.Done is not observed, but the running probes take ctx and abort
// on it, so a shutdown frees a slot promptly. Jobs still queued at shutdown are dropped
// on the floor: an unfinished probe has nothing to commit.
func (q *ProbeQueue) Run(ctx context.Context) {
	var g errgroup.Group
	g.SetLimit(probeConcurrency)
	for {
		select {
		case <-ctx.Done():
			_ = g.Wait()
			return
		case <-q.seed:
			g.Go(func() error { q.enqueueTargets(ctx); return nil })
		case job := <-q.jobs:
			g.Go(func() error { q.runJob(ctx, job); return nil })
		}
	}
}

// Seed asks the queue to probe every enabled indexer that is not currently healthy —
// the once-per-boot trigger, fired after HTTP readiness. It returns
// immediately: even the enumeration (a DB read per indexer) runs on the queue's own
// goroutines, so readiness never waits on it and shutdown still joins it.
func (q *ProbeQueue) Seed() {
	select {
	case q.seed <- struct{}{}:
	default: // already pending; one boot reconciliation is enough
	}
}

// Enqueue submits a fire-and-forget probe for slug. It never blocks and never reports
// a verdict — this is the create / credential-change trigger, where the write has
// already committed and a failing probe must record health without undoing anything.
func (q *ProbeQueue) Enqueue(slug string) { q.submit(probeJob{slug: slug}) }

// ProbeResult is one indexer's verdict from TestAll. Err is nil when the probe passed.
type ProbeResult struct {
	Slug string
	Err  error
}

// TestAll probes every slug through this same queue and reports one result per
// indexer. One indexer's failure never aborts the batch — every slug gets a slot, pass
// or fail — and the fleet-wide burst can never exceed probeConcurrency in flight, which
// is the whole point of moving "Test all" off the browser's unbounded fan-out (#485).
//
// A cancelled ctx (the operator navigated away mid-run) stops the WAIT, not the work:
// the probes already queued keep running on the queue's own lifetime, so a half-watched
// batch still records everything it learned instead of orphaning it.
func (q *ProbeQueue) TestAll(ctx context.Context, slugs []string) ([]ProbeResult, error) {
	results := make([]ProbeResult, len(slugs))
	done := make(chan struct{}, len(slugs))
	pending := 0
	for i, slug := range slugs {
		results[i].Slug = slug
		if !q.submit(probeJob{slug: slug, done: func(err error) {
			results[i].Err = err
			done <- struct{}{}
		}}) {
			results[i].Err = ErrProbeQueueFull
			continue
		}
		pending++
	}
	for range pending {
		select {
		case <-done:
		case <-ctx.Done():
			return nil, fmt.Errorf("registry: test all: %w", ctx.Err())
		}
	}
	return results, nil
}

// submit hands job to a worker, reporting whether it was accepted. It is deliberately
// non-blocking: an Add/Update must return to its caller without waiting on a login, so
// a saturated queue drops the probe (loudly) rather than stalling a write.
func (q *ProbeQueue) submit(job probeJob) bool {
	select {
	case q.jobs <- job:
		return true
	default:
		q.log.Warn().Str("indexer", job.slug).Msg("registry: probe queue full; probe dropped")
		return false
	}
}

// runJob probes one indexer and reports its verdict. A failure is logged (redacted — a
// login error routinely embeds a tracker URL with a passkey in it) and handed to the
// job's done callback if it has one; Test has already recorded the health event, which
// is the durable half.
func (q *ProbeQueue) runJob(ctx context.Context, job probeJob) {
	err := q.probe(ctx, job.slug)
	if err != nil {
		q.log.Debug().Str("indexer", job.slug).Str("error", apphttp.RedactError(err)).
			Msg("registry: indexer probe failed")
	}
	if job.done != nil {
		job.done(err)
	}
}

// enqueueTargets is the boot reconciliation: probe every enabled indexer that is not
// healthy, so an operator sees where the fleet stands instead of a wall of question
// marks that only a manual Test can clear. It deliberately does NOT skip a
// breaker-disabled or failing indexer (Test bypasses the breaker for exactly this
// reason) — after a restart, finding out whether a tracker recovered is the entire
// point, and for an indexer no feed will query while it reads failing it is the only
// way that can ever happen.
func (q *ProbeQueue) enqueueTargets(ctx context.Context) {
	slugs, err := q.targets(ctx)
	if err != nil {
		q.log.Warn().Str("error", apphttp.RedactError(err)).
			Msg("registry: boot probe: reading indexer health failed")
		return
	}
	if len(slugs) == 0 {
		return
	}
	q.log.Info().Int("indexers", len(slugs)).Msg("registry: probing indexers that are not healthy")
	for _, slug := range slugs {
		q.submit(probeJob{slug: slug})
	}
}

// ProbeTargets is the boot probe's work list: every ENABLED indexer that does not
// currently derive healthy.
//
// Selecting on "not healthy" rather than on unknown-or-breaker-open is what keeps the
// failing states escapable. Health is sticky (#484), so a failing indexer stays failing
// until something succeeds — and statusMembers excludes it from the status:healthy
// feed, so nothing queries it to produce that success. A failure nothing classified
// (rule 3: queried, never succeeded) has no health event and no breaker window either,
// so the old selection skipped it as well: fail once, never recover. A probe is the
// only traffic such an indexer can get.
//
// Disabled indexers are skipped outright — harbrr serves no search through them, so
// spending a login to learn their health buys nothing.
//
// It reuses AllStatuses rather than re-deriving, so the probe list and the dashboard
// can never disagree about which indexers are unhealthy.
func (r *StatsReporter) ProbeTargets(ctx context.Context) ([]string, error) {
	all, err := r.AllStatuses(ctx)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, st := range all {
		if st.Enabled && st.Status != StatusHealthy {
			out = append(out, st.Slug)
		}
	}
	return out, nil
}
