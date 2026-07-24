package registry

import (
	"context"
	"fmt"

	"github.com/autobrr/harbrr/internal/database"
	apphttp "github.com/autobrr/harbrr/internal/http"
)

// RehydrateCounters folds the persisted per-instance hit/miss/suppressed counters onto
// the in-memory atomics and the global totals, so the cache stats surface continues
// across a restart instead of resetting to zero. Called once after the cache is built
// (mirrors LoadOverrides); on success it sets countersRehydrated.
//
// The add is intentional (not a Store): at boot the atomics are zero, so adding the
// stored totals restores them exactly; on a self-heal retry after a failed boot load
// (see FlushCounters) the atomics already hold this session's own increments, so adding
// the restored totals yields the true sum rather than discarding the session's counts.
// The countersRehydrated gate guarantees it takes effect at most once, so it never
// double-counts. A load failure is returned (non-fatal at the call site).
func (c *SearchCache) RehydrateCounters(ctx context.Context) error {
	rows, err := c.counterStore.AllCounters(ctx, c.db)
	if err != nil {
		return fmt.Errorf("registry: load cache counters: %w", err)
	}
	var sumHits, sumMisses, sumSuppressed int64
	for _, r := range rows {
		ic := c.counters(r.InstanceID)
		ic.hits.Add(r.Hits)
		ic.misses.Add(r.Misses)
		ic.suppressed.Add(r.Suppressed)
		sumHits += r.Hits
		sumMisses += r.Misses
		sumSuppressed += r.Suppressed
	}
	c.hits.Add(sumHits)
	c.misses.Add(sumMisses)
	c.breakerSuppressed.Add(sumSuppressed)
	c.countersRehydrated.Store(true)
	return nil
}

// ForgetInstance drops a deleted instance's in-memory counters: it subtracts the
// instance's counts from the global atomics (keeping the global = sum-of-rows
// invariant) and removes the per-instance entry. Without this the deleted instance
// would keep over-reporting in the globals, keep appearing in StatsByInstance, and
// make FlushCounters re-attempt a doomed Upsert against its cascade-deleted row every
// cleanup tick. The durable cache_counters row is already gone via ON DELETE CASCADE.
// It also drops the instance's negative-breaker entry (an instance can have a breaker
// entry regardless of whether it has a counters entry, so this runs unconditionally,
// before the counters early-return) so a re-added instance that reuses the row id never
// replays the deleted instance's pre-delete error.
func (c *SearchCache) ForgetInstance(instanceID int64) {
	c.breaker.forget(instanceID)
	v, ok := c.instCounters.LoadAndDelete(instanceID)
	if !ok {
		return
	}
	ic, _ := v.(*instanceCounters)
	// Remove the entry FIRST (LoadAndDelete), then Swap each counter to zero and
	// subtract exactly what we captured. This closes the snapshot-then-Delete window
	// where a concurrent increment landed between reading the snapshot and the Delete
	// and was left in the global with no surviving row to back it. A reader that
	// already holds this pointer and increments past the Swap is a vanishingly narrow
	// residual (the deleted instance is no longer routed — manage.go invalidates the
	// engine before this), acceptable for single-user.
	c.hits.Add(-ic.hits.Swap(0))
	c.misses.Add(-ic.misses.Swap(0))
	c.breakerSuppressed.Add(-ic.suppressed.Swap(0))
}

// FlushCounters writes the live per-instance counters to the store so they survive a
// restart. It writes ABSOLUTE cumulative values (the atomics already hold the
// rehydrated total), so the UPSERT is idempotent — no delta tracking, no reset.
// Called on the cleanup tick and at shutdown, beside FlushTouches.
//
// Best-effort PER ROW like FlushTouches: a failure is logged (instance id + redacted
// error) and the next instance still flushes. A just-deleted instance's row has
// already cascaded away, so re-inserting it raises an FK error that is logged and
// skipped rather than aborting every other instance's flush (which a single
// transaction would).
//
// If a boot RehydrateCounters never succeeded (e.g. a transient DB error), this retries
// it here so a recovered DB resumes persistence instead of staying a no-op for the whole
// session. Until a load succeeds it must NOT flush: the atomics don't yet include the
// stored totals, so an absolute write would clobber them with this session's partial
// counts. RehydrateCounters is additive and gated, so the retry folds the session's own
// increments onto the restored totals exactly once.
func (c *SearchCache) FlushCounters(ctx context.Context) {
	if !c.countersRehydrated.Load() {
		if err := c.RehydrateCounters(ctx); err != nil {
			c.log.Warn().Str("error", apphttp.RedactError(err)).
				Msg("registry: cache counter rehydrate retry failed; skipping flush")
			return
		}
	}
	now := c.clock()
	c.instCounters.Range(func(k, v any) bool {
		id, _ := k.(int64)
		ic, _ := v.(*instanceCounters)
		row := database.CacheCounter{
			InstanceID: id,
			Hits:       ic.hits.Load(),
			Misses:     ic.misses.Load(),
			Suppressed: ic.suppressed.Load(),
			UpdatedAt:  now,
		}
		if err := c.counterStore.Upsert(ctx, c.db, row); err != nil {
			c.log.Warn().Int64("instance_id", id).Str("error", apphttp.RedactError(err)).
				Msg("registry: search cache counter flush failed")
		}
		return true
	})
}
