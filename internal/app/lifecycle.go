package app

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/rs/zerolog"

	"github.com/autobrr/harbrr/internal/database"
	"github.com/autobrr/harbrr/internal/indexer/registry"
)

// reap is the single skeleton behind every maintenance goroutine (session and
// search-cache reaping, indexer stat flushes, health-event retention): a timer
// loop that runs tick on each cycle and, once, final on shutdown.
//
// interval is read fresh at the top of every cycle rather than captured once,
// so a live-tunable source (the search cache's runtime cleanup_interval) keeps
// applying without a restart; a fixed reaper just passes a constant func.
// final runs with its own fresh 5s context.WithoutCancel-based timeout, since
// ctx is already cancelled by the time it fires — this is where the search
// cache and indexer stats commit their buffered writes before the composition
// root closes the database. wg lets the caller join every reaper before that
// close (see App.Run).
func reap(ctx context.Context, wg *sync.WaitGroup, interval func() time.Duration, tick, final func(ctx context.Context)) {
	wg.Add(1)
	go func() {
		defer wg.Done()
		t := time.NewTimer(interval())
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				if final != nil {
					fctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
					final(fctx)
					cancel()
				}
				return
			case <-t.C:
				tick(ctx)
				t.Reset(interval())
			}
		}
	}()
}

// fixedInterval adapts a constant duration to reap's re-read-each-cycle interval func.
func fixedInterval(d time.Duration) func() time.Duration {
	return func() time.Duration { return d }
}

// startReapers launches every periodic maintenance goroutine (session and cache
// reaping, stat flushes, health-event retention) on the shared shutdown WaitGroup,
// so App.Run joins them all before closing the database.
func startReapers(ctx context.Context, wg *sync.WaitGroup, db *database.DB,
	store *database.SessionStore, sc *registry.SearchCache, reg *registry.Registry, log zerolog.Logger,
) {
	startSessionCleanup(ctx, wg, store, log)
	startSearchCacheCleanup(ctx, wg, sc, log)
	startIndexerStatsFlush(ctx, wg, reg)
	startHealthEventCleanup(ctx, wg, db, log)
}

// startSessionCleanup reaps expired sessions hourly until ctx is cancelled.
func startSessionCleanup(ctx context.Context, wg *sync.WaitGroup, store *database.SessionStore, log zerolog.Logger) {
	reap(ctx, wg, fixedInterval(time.Hour), func(ctx context.Context) {
		if err := store.DeleteExpired(ctx); err != nil && !errors.Is(err, context.Canceled) {
			log.Warn().Err(err).Msg("session cleanup failed")
		}
	}, nil)
}

// Health-event retention. The append-only indexer_health_events table has no other
// bound, so a chronically-broken indexer polled every 15 min would grow it ~35k
// rows/year forever. A generous 90-day window keeps a full quarter of history for the
// dashboard (which reads the whole table per all-indexers load) while capping growth;
// a daily reap is ample for such low-frequency rows. A const is proportionate here —
// making the window configurable is a possible follow-up, not needed for this.
const (
	healthEventRetention       = 90 * 24 * time.Hour
	healthEventCleanupInterval = 24 * time.Hour
)

// startHealthEventCleanup reaps health events older than healthEventRetention once a
// day until ctx is cancelled, mirroring startSessionCleanup.
func startHealthEventCleanup(ctx context.Context, wg *sync.WaitGroup, db *database.DB, log zerolog.Logger) {
	reap(ctx, wg, fixedInterval(healthEventCleanupInterval), func(ctx context.Context) {
		cutoff := time.Now().Add(-healthEventRetention)
		if _, err := (database.Health{}).DeleteBefore(ctx, db, cutoff); err != nil && !errors.Is(err, context.Canceled) {
			log.Warn().Err(err).Msg("health event cleanup failed")
		}
	}, nil)
}

// indexerStatsFlushInterval is how often the per-indexer stat counters are flushed to
// the DB. A fixed 60s tick is fine: the counters are observability-only, so losing the
// increments since the last tick on a hard crash is acceptable (same tolerance the
// cache counters accept).
const indexerStatsFlushInterval = 60 * time.Second

// startIndexerStatsFlush periodically flushes the registry's durable per-indexer stat
// counters until ctx is cancelled, mirroring startSearchCacheCleanup. On shutdown it
// runs a final flush so the shutdown counters commit before the database closes
// (App.Run joins this goroutine first).
func startIndexerStatsFlush(ctx context.Context, wg *sync.WaitGroup, reg *registry.Registry) {
	reap(ctx, wg, fixedInterval(indexerStatsFlushInterval), func(ctx context.Context) {
		reg.FlushStats(ctx)
	}, func(ctx context.Context) {
		reg.FlushStats(ctx)
	})
}

// startSearchCacheCleanup reaps expired cache entries until ctx is cancelled,
// mirroring startSessionCleanup. The interval is re-read from the cache's live config
// each cycle (via cleanupTickInterval), so a runtime cleanup_interval change applies
// without a restart — eventually, on the next cycle (a change made mid-cycle waits out
// the current timer rather than interrupting it). A failed purge is logged (redacted)
// and never fails anything. On shutdown it runs a final flush of buffered hit bumps and
// stat counters, so App.Run's join-then-close ordering commits them before the database
// closes.
func startSearchCacheCleanup(ctx context.Context, wg *sync.WaitGroup, sc *registry.SearchCache, log zerolog.Logger) {
	reap(ctx, wg, func() time.Duration { return cleanupTickInterval(sc) }, func(ctx context.Context) {
		sc.FlushTouches(ctx)
		sc.FlushCounters(ctx)
		if _, err := sc.CleanupExpired(ctx); err != nil && !errors.Is(err, context.Canceled) {
			log.Warn().Err(err).Msg("search cache cleanup failed")
		}
	}, func(ctx context.Context) {
		sc.FlushTouches(ctx)
		sc.FlushCounters(ctx)
	})
}

// cleanupTickInterval reads the cache's live cleanup interval and keeps the reap loop
// from spinning: a non-positive value (unset) defaults to 1h, and a positive value
// below registry.MinCleanupInterval is floored to it. Config validation already
// enforces the same floor for API-set values; this also guards a config-file seed,
// which bypasses validation.
func cleanupTickInterval(sc *registry.SearchCache) time.Duration {
	d := sc.CleanupInterval()
	switch {
	case d <= 0:
		return time.Hour
	case d < registry.MinCleanupInterval:
		return registry.MinCleanupInterval
	default:
		return d
	}
}
