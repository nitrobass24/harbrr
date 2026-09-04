package registry

import (
	"context"
	"sync"
	"time"

	"github.com/rs/zerolog"

	"github.com/autobrr/harbrr/internal/database"
	"github.com/autobrr/harbrr/internal/database/dbinterface"
	apphttp "github.com/autobrr/harbrr/internal/http"
)

// budgetKind distinguishes the query and grab counters, which are configured,
// counted, and reactively learned independently (mirroring Prowlarr's separate
// queryLimit/grabLimit fields).
type budgetKind int

const (
	budgetKindQuery budgetKind = iota
	budgetKindGrab
)

// budgetDayFormat / budgetHourFormat key a rolling period to a UTC calendar day or
// hour. Comparing these strings against the current period is how rollover is
// detected — no background sweep needed: a stale period simply compares unequal on
// the next check and resets in place.
const (
	budgetDayFormat  = "2006-01-02"
	budgetHourFormat = "2006-01-02T15"
)

// RequestBudget enforces the per-indexer, cap-aware request budget: an operator can
// set a queryLimit/grabLimit (+ limitsUnit day|hour) per instance via the existing
// generic instance-settings mechanism (the reserved query_limit/grab_limit/
// limits_unit settings, resolved into a budgetLimits at build time exactly like the
// freeleech/cache_ttl settings), and the registry additionally LEARNS a cap
// reactively when a tracker's own quota error is observed, even with no operator
// config at all.
//
// Durability: unlike IndexerStats/SearchCache's counters (rehydrate-at-boot,
// periodic-flush), this writes through to the store synchronously on every mutating
// call. ponytail: budget checks are not the hot path (already paced to ~1/host/sec
// by pacedclient), so the extra query per outbound hit is cheap, and it skips wiring
// a boot rehydrate + shutdown flush into cmd/harbrr. Revisit with a buffered flush if
// profiling ever shows this write is a bottleneck.
type RequestBudget struct {
	store database.BudgetCountersStore
	db    dbinterface.Execer
	clock func() time.Time
	log   zerolog.Logger

	states sync.Map // map[int64]*budgetState
}

// budgetState is one instance's in-memory query/grab counters, guarded by its own
// mutex so concurrent Reserve/MarkQuotaSpent calls for the SAME instance serialize
// (different instances never contend). loaded gates the one-time read-through from
// the store on first touch.
type budgetState struct {
	mu     sync.Mutex
	loaded bool

	queryPeriod    string
	queryCount     int64
	queryExhausted bool

	grabPeriod    string
	grabCount     int64
	grabExhausted bool
}

// newRequestBudget builds the budget tracker over db with the given clock/logger.
func newRequestBudget(db dbinterface.Execer, clock func() time.Time, log zerolog.Logger) *RequestBudget {
	if clock == nil {
		clock = time.Now
	}
	return &RequestBudget{db: db, clock: clock, log: log}
}

// stateFor returns (creating on first use) the in-memory state for instanceID.
func (b *RequestBudget) stateFor(instanceID int64) *budgetState {
	v, _ := b.states.LoadOrStore(instanceID, &budgetState{})
	st, _ := v.(*budgetState)
	return st
}

// ensureLoaded reads instanceID's persisted counters into st on first touch. Must be
// called with st.mu held. A read failure is logged and treated as "no row yet" (fail
// open — the budget starts from zero rather than blocking every request).
func (b *RequestBudget) ensureLoaded(ctx context.Context, instanceID int64, st *budgetState) {
	if st.loaded {
		return
	}
	if b.db == nil {
		// No store wired (e.g. a cache-less test adapter): stay in-memory only,
		// starting from zero — fail open rather than panic on a nil Execer.
		st.loaded = true
		return
	}
	row, ok, err := b.store.Get(ctx, b.db, instanceID)
	switch {
	case err != nil:
		b.log.Warn().Int64("instance_id", instanceID).Str("error", apphttp.RedactError(err)).
			Msg("registry: budget counters read failed; starting from zero")
	case ok:
		st.queryPeriod, st.queryCount, st.queryExhausted = row.QueryPeriod, row.QueryCount, row.QueryExhausted
		st.grabPeriod, st.grabCount, st.grabExhausted = row.GrabPeriod, row.GrabCount, row.GrabExhausted
	}
	st.loaded = true
}

// ReserveQuery reports whether a query is allowed to reach the tracker right now,
// counting it against the budget if so. limits is the instance's resolved budget
// knobs (resolveBudgetLimits) — the adapter's build-time snapshot on the serve
// path, a fresh per-read view on the stats path.
func (b *RequestBudget) ReserveQuery(ctx context.Context, instanceID int64, limits budgetLimits, now time.Time) bool {
	return b.reserve(ctx, instanceID, limits, budgetKindQuery, now)
}

// ReserveGrab is ReserveQuery for the grab budget (grab_limit).
func (b *RequestBudget) ReserveGrab(ctx context.Context, instanceID int64, limits budgetLimits, now time.Time) bool {
	return b.reserve(ctx, instanceID, limits, budgetKindGrab, now)
}

// ReleaseQuery gives back a unit reserved by ReserveQuery when the request never
// reached the tracker (autobrr/harbrr#489's reachedTracker shapes: an *arr hanging up,
// a user navigating away from a /dl link, the pacing budget never granting a token).
// reservedAt MUST be the same timestamp the reservation was made with — it is what
// pins the refund to the period the unit was counted under.
func (b *RequestBudget) ReleaseQuery(ctx context.Context, instanceID int64, limits budgetLimits, reservedAt time.Time) {
	b.release(ctx, instanceID, limits, budgetKindQuery, reservedAt)
}

// ReleaseGrab is ReleaseQuery for the grab budget.
func (b *RequestBudget) ReleaseGrab(ctx context.Context, instanceID int64, limits budgetLimits, reservedAt time.Time) {
	b.release(ctx, instanceID, limits, budgetKindGrab, reservedAt)
}

// release is the shared refund for both kinds: it decrements kind's counter ONLY while
// the stored period is still the one reservedAt fell in. A period that has rolled over
// (or been rolled forward by a concurrent reserve) already dropped the reservation, so
// decrementing then would steal from the fresh period's allowance — the refund is
// simply dropped instead. The exhausted latch is deliberately untouched: handing a unit
// back is not evidence the tracker's own cap lifted.
func (b *RequestBudget) release(ctx context.Context, instanceID int64, limits budgetLimits, kind budgetKind, reservedAt time.Time) {
	// The canonical caller is a request whose context just died (a hung-up *arr), and
	// the refund still has to reach the store — persisting it under that dead context
	// would fail every time and leave the durable counter over-counted until restart.
	ctx = context.WithoutCancel(ctx)

	st := b.stateFor(instanceID)
	st.mu.Lock()
	defer st.mu.Unlock()
	b.ensureLoaded(ctx, instanceID, st)

	period := periodKey(reservedAt, limits.unit)
	count, exhausted, curPeriod := st.snapshot(kind)
	if curPeriod != period || count <= 0 {
		return
	}
	st.set(kind, period, count-1, exhausted)
	// Under st.mu for the same write-ordering reason as reserve.
	b.persist(ctx, st.row(instanceID, b.clock()))
}

// reserve is the shared count-and-check for both kinds: it rolls the counter over to
// a fresh period when the period key has changed (which also clears any
// reactively-learned exhausted latch — a new day/hour is a clean slate even if the
// tracker refused yesterday), then allows the call when neither the learned-exhausted
// latch nor a configured limit blocks it, incrementing the count on an allow. A nil
// (unset) limit means the budget is disabled for that kind — only the reactive
// learning latch can still block it.
func (b *RequestBudget) reserve(ctx context.Context, instanceID int64, limits budgetLimits, kind budgetKind, now time.Time) bool {
	st := b.stateFor(instanceID)
	st.mu.Lock()
	b.ensureLoaded(ctx, instanceID, st)

	period := periodKey(now, limits.unit)
	count, exhausted, curPeriod := st.snapshot(kind)
	if curPeriod != period {
		count, exhausted = 0, false
	}

	limit := limits.limit(kind)
	allow := !exhausted && (limit == nil || count < int64(*limit))
	if allow {
		count++
	}
	st.set(kind, period, count, exhausted)
	// Persisted under st.mu so snapshots reach the store in mutation order —
	// unlocking first would let two concurrent reserves persist in reverse and
	// leave a stale count (or a dropped exhausted latch) for the next process
	// start to reload.
	b.persist(ctx, st.row(instanceID, b.clock()))
	st.mu.Unlock()
	return allow
}

// MarkQuotaSpent is the reactive-learning entry point (autobrr/harbrr#251's
// differentiator vs Prowlarr's blind backoff): called when a tracker responds with
// its own declared quota-cap error (e.g. newznab code 910), it marks kind exhausted
// for the CURRENT period regardless of any configured limit — so harbrr stops
// issuing outbound requests of that kind against this indexer until the unit rolls
// over, even though it was never told the tracker's exact numeric cap.
func (b *RequestBudget) MarkQuotaSpent(ctx context.Context, instanceID int64, limits budgetLimits, kind budgetKind, now time.Time) {
	st := b.stateFor(instanceID)
	st.mu.Lock()
	b.ensureLoaded(ctx, instanceID, st)

	period := periodKey(now, limits.unit)
	count, _, curPeriod := st.snapshot(kind)
	if curPeriod != period {
		count = 0
	}
	st.set(kind, period, count, true)
	// Under st.mu for the same write-ordering reason as reserve.
	b.persist(ctx, st.row(instanceID, b.clock()))
	st.mu.Unlock()
}

// persist writes row to the store, best-effort: a failure is logged and the
// in-memory state (already updated) stays authoritative for this process's
// lifetime, matching the fail-open posture of the rest of the counter stores.
func (b *RequestBudget) persist(ctx context.Context, row database.BudgetCounter) {
	if b.db == nil {
		return
	}
	if err := b.store.Upsert(ctx, b.db, row); err != nil {
		b.log.Warn().Int64("instance_id", row.InstanceID).Str("error", apphttp.RedactError(err)).
			Msg("registry: budget counters persist failed")
	}
}

// snapshot returns kind's current count, exhausted latch, and period key. Caller
// must hold st.mu.
func (st *budgetState) snapshot(kind budgetKind) (count int64, exhausted bool, period string) {
	if kind == budgetKindGrab {
		return st.grabCount, st.grabExhausted, st.grabPeriod
	}
	return st.queryCount, st.queryExhausted, st.queryPeriod
}

// set overwrites kind's period/count/exhausted. Caller must hold st.mu.
func (st *budgetState) set(kind budgetKind, period string, count int64, exhausted bool) {
	if kind == budgetKindGrab {
		st.grabPeriod, st.grabCount, st.grabExhausted = period, count, exhausted
		return
	}
	st.queryPeriod, st.queryCount, st.queryExhausted = period, count, exhausted
}

// row snapshots st into a database.BudgetCounter ready to persist. Caller must hold
// st.mu.
func (st *budgetState) row(instanceID int64, now time.Time) database.BudgetCounter {
	return database.BudgetCounter{
		InstanceID:     instanceID,
		QueryPeriod:    st.queryPeriod,
		QueryCount:     st.queryCount,
		QueryExhausted: st.queryExhausted,
		GrabPeriod:     st.grabPeriod,
		GrabCount:      st.grabCount,
		GrabExhausted:  st.grabExhausted,
		UpdatedAt:      now,
	}
}

// BudgetKindStatus is one kind's (query or grab) standing in the CURRENT period:
// Used is what has been counted so far, Limit is the configured cap (0 = none
// configured), Detected says that cap was read from the indexer's own account limits
// rather than typed by the operator (autobrr/harbrr#377), and Learned reports the
// reactive latch — the tracker declared its own quota spent, which carries no number
// because the cap was never declared. The three cap origins stay separate fields on
// purpose: an operator must not mistake a learned or detected cap for one they set
// (autobrr/harbrr#402).
type BudgetKindStatus struct {
	Used     int64
	Limit    int
	Detected bool
	Learned  bool
}

// BudgetStatus is one instance's request-budget standing for the current rolling
// period — the read-only view behind the usage meter. Unit is "day" or "hour";
// PeriodEnd is when the current period rolls over (UTC), i.e. when Used resets and
// any learned latch clears.
type BudgetStatus struct {
	Unit      string
	PeriodEnd time.Time
	Query     BudgetKindStatus
	Grab      BudgetKindStatus
}

// Status reports instanceID's current-period budget standing WITHOUT counting
// anything: it is the observability read behind the usage meter and never mutates or
// persists. It does take the same lazy read-through any other first touch takes
// (ensureLoaded), so a meter opened after a restart reports the durable counters
// rather than a false zero. A stored period that is no longer current reads as a
// fresh one (0 used, no latch) — exactly what reserve would roll it over to on the
// next request, without writing that rollover here.
func (b *RequestBudget) Status(ctx context.Context, instanceID int64, limits budgetLimits, now time.Time) BudgetStatus {
	st := b.stateFor(instanceID)
	st.mu.Lock()
	defer st.mu.Unlock()
	b.ensureLoaded(ctx, instanceID, st)

	period := periodKey(now, limits.unit)
	return BudgetStatus{
		Unit:      limits.unit,
		PeriodEnd: periodEnd(now, limits.unit),
		Query:     kindStatus(st, budgetKindQuery, limits, period),
		Grab:      kindStatus(st, budgetKindGrab, limits, period),
	}
}

// kindStatus reads one kind's live view: the stored count and learned latch while the
// stored period is still current, zeroes once it has rolled over. Caller must hold
// st.mu.
func kindStatus(st *budgetState, kind budgetKind, limits budgetLimits, period string) BudgetKindStatus {
	count, exhausted, curPeriod := st.snapshot(kind)
	if curPeriod != period {
		count, exhausted = 0, false
	}
	out := BudgetKindStatus{Used: count, Learned: exhausted}
	if limit := limits.limit(kind); limit != nil {
		out.Limit = *limit
		out.Detected = limits.detected(kind)
	}
	return out
}

// ForgetInstance drops a deleted instance's in-memory budget state (mirrors
// IndexerStats.ForgetInstance); the durable row is already gone via ON DELETE
// CASCADE.
func (b *RequestBudget) ForgetInstance(instanceID int64) {
	b.states.LoadAndDelete(instanceID)
}

// periodKey formats now (converted to UTC) as the rolling-period boundary key for
// unit: a UTC calendar day, or (opt-in) a UTC calendar hour. ponytail: this is a
// FIXED calendar-boundary window (UTC midnight / top of hour), not a true sliding
// 24h/1h window — matching the issue's explicit "reset at UTC midnight for daily"
// ask. A sliding window would need per-request timestamps, not a counter.
func periodKey(now time.Time, unit string) string {
	now = now.UTC()
	if unit == "hour" {
		return now.Format(budgetHourFormat)
	}
	return now.Format(budgetDayFormat)
}

// periodEnd is when the period CONTAINING now ends: the next UTC midnight for a day
// unit, the top of the next UTC hour for an hour unit. Derived from now rather than
// from a stored period key, so a state left behind in an old period still reports the
// boundary the next request would actually roll over at.
func periodEnd(now time.Time, unit string) time.Time {
	now = now.UTC()
	if unit == "hour" {
		return now.Truncate(time.Hour).Add(time.Hour)
	}
	return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC).AddDate(0, 0, 1)
}
