package registry

import (
	"context"
	"fmt"

	"github.com/autobrr/harbrr/internal/database"
	apphttp "github.com/autobrr/harbrr/internal/http"
)

// RehydrateCounters loads the persisted per-instance hit/miss/suppressed counters
// into the in-memory atomics at boot and seeds the global atomics with their sum, so
// the cache stats surface continues across a restart instead of resetting to zero.
// Called once after the cache is built (mirrors LoadOverrides), before any traffic.
//
// On success it sets countersRehydrated, which gates FlushCounters: until a load has
// succeeded, a flush stays a no-op so an empty/failed read can never overwrite the
// stored totals with zeroes. A load failure is returned (non-fatal at the call site).
func (c *SearchCache) RehydrateCounters(ctx context.Context) error {
	rows, err := c.counterStore.AllCounters(ctx, c.db)
	if err != nil {
		return fmt.Errorf("registry: load cache counters: %w", err)
	}
	var sumHits, sumMisses, sumSuppressed int64
	for _, r := range rows {
		ic := c.counters(r.InstanceID)
		ic.hits.Store(r.Hits)
		ic.misses.Store(r.Misses)
		ic.suppressed.Store(r.Suppressed)
		sumHits += r.Hits
		sumMisses += r.Misses
		sumSuppressed += r.Suppressed
	}
	c.hits.Store(sumHits)
	c.misses.Store(sumMisses)
	c.breakerSuppressed.Store(sumSuppressed)
	c.countersRehydrated.Store(true)
	return nil
}

// ForgetInstance drops a deleted instance's in-memory counters: it subtracts the
// instance's counts from the global atomics (keeping the global = sum-of-rows
// invariant) and removes the per-instance entry. Without this the deleted instance
// would keep over-reporting in the globals, keep appearing in StatsByInstance, and
// make FlushCounters re-attempt a doomed Upsert against its cascade-deleted row every
// cleanup tick. The durable cache_counters row is already gone via ON DELETE CASCADE.
func (c *SearchCache) ForgetInstance(instanceID int64) {
	hits, misses, suppressed := c.instanceSnapshot(instanceID)
	c.hits.Add(-hits)
	c.misses.Add(-misses)
	c.breakerSuppressed.Add(-suppressed)
	c.instCounters.Delete(instanceID)
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
// transaction would). It is a no-op until RehydrateCounters has succeeded.
func (c *SearchCache) FlushCounters(ctx context.Context) {
	if !c.countersRehydrated.Load() {
		c.log.Warn().Msg("registry: skipping cache counter flush; counters not yet rehydrated")
		return
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
