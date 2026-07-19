package registry

import (
	"context"
	"time"

	"github.com/rs/zerolog"

	"github.com/autobrr/harbrr/internal/domain"
	apphttp "github.com/autobrr/harbrr/internal/http"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
	"github.com/autobrr/harbrr/internal/indexer/core"
)

// WarmTickInterval is the RSS warm-cache poller's fixed scheduler granularity —
// checked every minute for due targets. Exported so app/lifecycle.go's reap() call
// can pass it directly without a bespoke registry.StartWarmer wrapper.
const (
	WarmTickInterval = time.Minute
	warmMinInterval  = 10 * time.Minute
	warmMaxInterval  = 120 * time.Minute

	// warmIntervalSetting is the reserved per-instance setting name — a Go duration
	// string like "15m", read exactly like "rate_interval"/"cache_ttl".
	warmIntervalSetting = "rss_warm_interval"
)

// warmTarget is one opted-in indexer instance due for periodic RSS warming.
type warmTarget struct {
	instanceID int64
	slug       string
	interval   time.Duration
}

// Warmer drives the served path for each opted-in indexer at its configured
// interval, keeping the canonical RSS cache entry (#257) hot so every downstream
// RSS poll stays a pure cache read (#252; ADR 0005). Its seams are funcs, not
// concrete types, so TickOnce is unit-testable with fakes. nextDue is touched only
// from the single reap goroutine that drives TickOnce, so it needs no lock.
type Warmer struct {
	resolve func(context.Context, string) (core.Indexer, bool)
	targets func(context.Context) []warmTarget
	clock   func() time.Time
	log     zerolog.Logger

	nextDue map[int64]time.Time
}

// NewWarmer builds a Warmer over reg's live resolve/target seams.
func NewWarmer(reg *Registry, clock func() time.Time, log zerolog.Logger) *Warmer {
	return &Warmer{
		resolve: reg.Indexer,
		targets: reg.warmTargets,
		clock:   clock,
		log:     log,
		nextDue: make(map[int64]time.Time),
	}
}

// TickOnce runs one scheduler cycle: read the current opted-in targets, compute
// which are due, and warm each due slug. Called by app/lifecycle.go's reap every
// WarmTickInterval.
func (w *Warmer) TickOnce(ctx context.Context) {
	for _, slug := range w.schedule(w.clock(), w.targets(ctx)) {
		w.warmOne(ctx, slug)
	}
}

// schedule advances w.nextDue against targets and returns the slugs due to warm
// this tick, in target order. Deterministic given (now, targets, the existing
// nextDue state) — exercised directly in tests via a Warmer literal, no clock or
// goroutine involved.
//
// An instance seen for the first time seeds nextDue = now + warmPhase(instanceID,
// interval) — the D1 stagger — and is NOT due this tick: warming at boot would
// often just re-refresh an entry the SQLite-persisted cache already has fresh, and
// a cold entry is covered by ordinary pull-through on the first real poll. On every
// later tick, an instance is due once now reaches its nextDue; nextDue then
// advances by +interval from its own previous SCHEDULED value (not from "now"), so
// the phase is preserved forever — no drift, and no reconverging herd at later
// boundaries either. An instance no longer present in targets (removed, disabled,
// or its interval cleared) is pruned from nextDue, so a later re-enable seeds a
// fresh stagger rather than replaying a stale due time.
//
// After marking an instance due, nextDue is advanced past now (skipping ahead by
// whole intervals, not just one) before being stored. A single +interval step
// would leave nextDue in the past after the process was suspended (laptop/VM
// sleep) for longer than the interval, so every following 1-minute tick would see
// it as still-due and fire again until the missed backlog was replayed — one warm
// per skipped interval instead of the one refresh the cache actually needs. The
// skip-ahead preserves the original phase (still nextDue mod interval == the seed
// phase) while landing strictly in the future.
func (w *Warmer) schedule(now time.Time, targets []warmTarget) []string {
	seen := make(map[int64]bool, len(targets))
	var due []string
	for _, t := range targets {
		seen[t.instanceID] = true
		next, ok := w.nextDue[t.instanceID]
		if !ok {
			w.nextDue[t.instanceID] = now.Add(warmPhase(t.instanceID, t.interval))
			continue
		}
		if now.Before(next) {
			continue
		}
		due = append(due, t.slug)
		next = next.Add(t.interval)
		for !next.After(now) {
			next = next.Add(t.interval)
		}
		w.nextDue[t.instanceID] = next
	}
	for id := range w.nextDue {
		if !seen[id] {
			delete(w.nextDue, id)
		}
	}
	return due
}

// warmPhase returns a stable per-instance phase in [0, interval), minute-granular
// since WarmTickInterval is one minute. Spreading first-due times across the first
// interval — and keeping them spread at every later boundary, since schedule
// advances nextDue from its own scheduled value — avoids every opted-in indexer
// landing on the same instant and firing N concurrent engine builds/fetches
// through a shared proxy/FlareSolverr at every interval boundary.
//
// ponytail: instanceID % intervalMinutes can collide two instances into the same
// one-minute slot — still far better than all-N-at-once. Upgrade to an
// i·interval/n stable-ordering assignment only if even distribution ever actually
// matters at the scale harbrr runs at.
func warmPhase(instanceID int64, interval time.Duration) time.Duration {
	minutes := int64(interval / time.Minute)
	return time.Duration(instanceID%minutes) * time.Minute
}

// warmOne drives the served path for slug under cache bypass, keeping its
// canonical RSS cache entry hot. Any error — a disabled/unresolvable instance, an
// exhausted request budget (errBudgetExhausted), an open circuit breaker
// (errCircuitOpen), or a transport failure — is a logged skip: the warmer never
// retries within a tick, it just waits for the target's next scheduled warm. The
// error is redacted before logging since a native driver's transport error can
// embed a tracker URL (passkey included).
func (w *Warmer) warmOne(ctx context.Context, slug string) {
	idx, ok := w.resolve(ctx, slug)
	if !ok {
		w.log.Debug().Str("indexer", slug).Msg("registry: rss warm skipped: indexer unresolvable")
		return
	}
	if _, err := idx.Search(core.WithCacheBypass(ctx), search.Query{}); err != nil {
		w.log.Debug().Str("indexer", slug).Str("error", apphttp.RedactError(err)).
			Msg("registry: rss warm skipped")
	}
}

// warmTargets returns the current opted-in RSS warm-cache targets: every enabled
// instance with a valid "rss_warm_interval" setting. Gated on the search cache
// being configured AND runtime-enabled — with caching off, adapter.Search runs
// liveSearch directly and never stores anything, so a cache-bypass warm would be a
// live tracker hit whose result is thrown away outright (#252 D3 — a correctness
// gate, not just thrift). Re-read from the DB every tick rather than
// invalidation-driven: at single-user scale this is trivial and it picks up an
// interval/enable change immediately with no cache-invalidation wiring.
//
// ponytail: N+1 (one Settings query per instance). Upgrade to a single indexed
// query if the instance count ever grows enough for it to matter.
func (r *Resolver) warmTargets(ctx context.Context) []warmTarget {
	if r.searchCache == nil || !r.searchCache.Enabled() {
		return nil
	}
	instances, err := r.instances.List(ctx, r.db)
	if err != nil {
		r.log.Warn().Str("error", apphttp.RedactError(err)).Msg("registry: rss warm: list instances failed")
		return nil
	}
	var targets []warmTarget
	for _, inst := range instances {
		if !inst.Enabled {
			continue
		}
		settings, err := r.instances.Settings(ctx, r.db, inst.ID)
		if err != nil {
			r.log.Warn().Str("indexer", inst.Slug).Str("error", apphttp.RedactError(err)).
				Msg("registry: rss warm: read settings failed")
			continue
		}
		interval, ok := warmInterval(settings)
		if !ok {
			continue
		}
		targets = append(targets, warmTarget{instanceID: inst.ID, slug: inst.Slug, interval: interval})
	}
	return targets
}

// warmInterval reads and clamps the "rss_warm_interval" setting (a Go duration
// string, e.g. "15m" — a reserved setting like "rate_interval"/"cache_ttl").
// Absent, unparseable, or non-positive is disabled (ok=false) — opt-in,
// default-off. A parsed value is clamped to [warmMinInterval, warmMaxInterval].
func warmInterval(settings []domain.IndexerSetting) (time.Duration, bool) {
	for _, s := range settings {
		if s.Name != warmIntervalSetting {
			continue
		}
		d, err := time.ParseDuration(s.Value)
		if err != nil || d <= 0 {
			return 0, false
		}
		switch {
		case d < warmMinInterval:
			return warmMinInterval, true
		case d > warmMaxInterval:
			return warmMaxInterval, true
		default:
			return d, true
		}
	}
	return 0, false
}
