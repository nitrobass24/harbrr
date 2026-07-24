package registry

import (
	"context"
	"fmt"
	"sort"
	"time"

	apphttp "github.com/autobrr/harbrr/internal/http"
)

// SearchCacheStats is the management view of the cache: the durable row-derived
// figures plus the hit-ratio counters. Hits/Misses/HitRatio/BreakerSuppressed survive
// a restart (persisted via counterStore); the rest are read from the store — notably
// TotalHits is a live SUM over rows CURRENTLY cached, so it is NOT cumulative and
// falls (including to 0) whenever those rows are reaped.
type SearchCacheStats struct {
	Entries         int64
	TotalHits       int64
	ApproxSizeBytes int64
	OldestUnixSec   *int64
	NewestUnixSec   *int64
	LastUsedUnixSec *int64

	// Cumulative counters, persisted across restarts (see searchcache_counters.go).
	// A failed live search counts as neither hit nor miss; Flush resets them.
	Hits     int64
	Misses   int64
	HitRatio float64
	// Rolling 24h view of the same counters (in-memory only — empty after a restart).
	Hits24h     int64
	Misses24h   int64
	HitRatio24h float64
	// BreakerSuppressed counts MISSes short-circuited by an open negative breaker —
	// tracker requests the breaker spared.
	BreakerSuppressed int64
}

// InstanceCacheStats is one instance's merged cache observability: the durable
// row-derived figures (HitsSaved/Entries/ApproxSizeBytes) plus the in-memory counters
// (persisted across restarts) and the live breaker state. HitsSaved is a live SUM of
// per-entry hit counts over rows CURRENTLY cached for this instance — it is NOT
// cumulative and falls (including to 0) whenever those rows are reaped (cleanup,
// flush, or an invalidation). The headline "tracker requests this indexer served
// from cache" figure is Hits (the cumulative, restart-persisted counter below).
type InstanceCacheStats struct {
	InstanceID        int64
	Entries           int64
	HitsSaved         int64
	ApproxSizeBytes   int64
	Hits              int64
	Misses            int64
	BreakerSuppressed int64
	HitRatio          float64
	// BreakerOpenUntil is the instant the breaker reopens this instance to live
	// traffic, or nil when the breaker is currently closed for it.
	BreakerOpenUntil *int64
}

// Stats returns the cache statistics: durable store figures plus the in-memory
// hit-ratio. The store error wraps nothing secret (it has no payload to leak).
func (c *SearchCache) Stats(ctx context.Context) (SearchCacheStats, error) {
	// Drain buffered hit bumps first so the reported hit_count/last_used reflect
	// hits served since the last flush rather than lagging by a cleanup interval.
	c.FlushTouches(ctx)
	s, err := c.store.Stats(ctx, c.db)
	if err != nil {
		return SearchCacheStats{}, err //nolint:wrapcheck // store already wraps with context; no key/payload to add.
	}
	hits, misses := c.hits.Load(), c.misses.Load()
	hits24, misses24 := c.window.totals(c.clock())
	out := SearchCacheStats{
		Entries:           s.Entries,
		TotalHits:         s.TotalHits,
		ApproxSizeBytes:   s.ApproxSizeBytes,
		OldestUnixSec:     unixSecPtr(s.Oldest),
		NewestUnixSec:     unixSecPtr(s.Newest),
		LastUsedUnixSec:   unixSecPtr(s.LastUsed),
		Hits:              hits,
		Misses:            misses,
		HitRatio:          hitRatio(hits, misses),
		Hits24h:           hits24,
		Misses24h:         misses24,
		HitRatio24h:       hitRatio(hits24, misses24),
		BreakerSuppressed: c.breakerSuppressed.Load(),
	}
	return out, nil
}

// StatsByInstance returns one merged stats row per instance that has either durable
// cache entries or recorded in-memory traffic counters (the union of both sources),
// ordered by instance id. It folds the durable per-instance figures, the in-memory
// hit/miss/suppressed counters (persisted across restarts), and the live breaker
// open-state into one view for the
// per-indexer observability surface. Like Stats it flushes buffered touches first so
// HitsSaved reflects hits served since the last flush.
func (c *SearchCache) StatsByInstance(ctx context.Context) ([]InstanceCacheStats, error) {
	c.FlushTouches(ctx)
	durable, err := c.store.StatsByInstance(ctx, c.db)
	if err != nil {
		return nil, err //nolint:wrapcheck // store wraps with context; no key/payload to add.
	}
	merged := make(map[int64]*InstanceCacheStats, len(durable))
	for _, d := range durable {
		merged[d.InstanceID] = &InstanceCacheStats{
			InstanceID: d.InstanceID, Entries: d.Entries,
			HitsSaved: d.HitsSaved, ApproxSizeBytes: d.ApproxSizeBytes,
		}
	}
	now := c.clock()
	c.instCounters.Range(func(k, v any) bool {
		id, _ := k.(int64)
		ic, _ := v.(*instanceCounters)
		row := merged[id]
		if row == nil {
			row = &InstanceCacheStats{InstanceID: id}
			merged[id] = row
		}
		row.Hits = ic.hits.Load()
		row.Misses = ic.misses.Load()
		row.BreakerSuppressed = ic.suppressed.Load()
		row.HitRatio = hitRatio(row.Hits, row.Misses)
		return true
	})
	for id, row := range merged {
		if until := c.breaker.openUntil(id, now); !until.IsZero() {
			s := until.Unix()
			row.BreakerOpenUntil = &s
		}
	}
	return sortedInstanceStats(merged), nil
}

// sortedInstanceStats flattens the merge map into a slice ordered by instance id so
// the API surface and tests see a deterministic order.
func sortedInstanceStats(merged map[int64]*InstanceCacheStats) []InstanceCacheStats {
	out := make([]InstanceCacheStats, 0, len(merged))
	for _, row := range merged {
		out = append(out, *row)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].InstanceID < out[j].InstanceID })
	return out
}

// Flush deletes every cache entry and returns the count purged. It also resets the
// hit/miss/suppressed counters — in-memory, persisted, and the 24h window — so an
// operator flush starts the stats surface from a clean slate. The counters reset
// even if deleting the persisted rows fails (the next FlushCounters upserts the
// zeroed values anyway). Only Flush resets: the cleanup tick and per-instance
// invalidation never touch the counters (see TestHitsMonotoneAcrossCleanup, #350).
func (c *SearchCache) Flush(ctx context.Context) (int64, error) {
	n, err := c.store.Flush(ctx, c.db)
	if err != nil {
		return 0, err //nolint:wrapcheck // store wraps with context; nothing secret to add.
	}
	// Subtract each instance's swapped counts from the globals (mirrors
	// ForgetInstance) so a concurrent in-flight increment is never lost twice.
	c.instCounters.Range(func(_, v any) bool {
		ic, _ := v.(*instanceCounters)
		c.hits.Add(-ic.hits.Swap(0))
		c.misses.Add(-ic.misses.Swap(0))
		c.breakerSuppressed.Add(-ic.suppressed.Swap(0))
		return true
	})
	c.window.reset()
	if derr := c.counterStore.DeleteAll(ctx, c.db); derr != nil {
		c.log.Warn().Str("error", apphttp.RedactError(derr)).
			Msg("registry: reset persisted cache counters failed")
	}
	return n, nil
}

// ExpireAll marks every currently-live cache entry expired — WITHOUT deleting it —
// and returns the count affected. It backs boot-time def-content-change detection
// (EnsureDefsFingerprint in searchcache_config.go, autobrr/harbrr#347): a
// definitions upgrade or dropin edit must stop old-shape entries from serving
// immediately, but — unlike Flush — the rows must survive so the announce-source
// diff (priorGUIDs, via FetchAny) and the #251 budget-exhausted stale serve
// (fetchStale) keep reading them. cacheReapGrace still reaps them, just later than
// an ordinary TTL expiry would.
func (c *SearchCache) ExpireAll(ctx context.Context) (int64, error) {
	n, err := c.store.ExpireAll(ctx, c.db, c.clock())
	if err != nil {
		return 0, err //nolint:wrapcheck // store wraps with context; nothing secret to add.
	}
	return n, nil
}

// cacheReapGrace is how long an EXPIRED row is retained before the cleanup tick
// deletes it. Two features read expired rows by design and break when the reaper
// removes them too early: the announce-source diff (priorGUIDs reads the row a
// write-back overwrites — by definition expired on the miss path) and the
// budget-exhausted stale serve (#251's fetchStale, which must keep serving until the
// budget period resets — up to a full UTC day). 24h covers the longest budget period
// and dwarfs the 6h announce window. Serving is unaffected: Fetch filters on the real
// expires_at; retained rows are reachable only via FetchAny/fetchStale.
const cacheReapGrace = 24 * time.Hour

// CleanupExpired deletes every entry that has been expired for at least
// cacheReapGrace, returning the count purged. The background ticker calls it. The
// grace period means Stats().Entries/ApproxSizeBytes can include rows up to 24h past
// their expires_at — for a cache key that keeps being re-queried this is invisible
// (Store upserts the same PK row), so it only lingers rows for keys nobody re-queried
// since they expired.
func (c *SearchCache) CleanupExpired(ctx context.Context) (int64, error) {
	n, err := c.store.CleanupExpired(ctx, c.db, c.clock().Add(-cacheReapGrace))
	if err != nil {
		return 0, err //nolint:wrapcheck // store wraps with context; nothing secret to add.
	}
	return n, nil
}

// InvalidateByInstance purges every entry for one instance (called after a config
// mutation), returning the count purged. It bumps the instance's invalidation epoch
// BEFORE the DB purge (in addition to it): a store from an engine built before this
// call — a detached SWR refresh or an in-flight miss still holding the old adapter —
// then sees the advanced epoch in storeBestEffort and drops its write-back instead of
// resurrecting a stale-config entry. Bumping before the purge guarantees any store that
// observes the completed purge also observes the new epoch (U8R-F4). It also drops the
// instance's negative-breaker entry: the breaker is a negative-result cache, so the
// "a config change must never serve stale results" invariant covers replayed errors
// too — after a credential fix, the next miss must probe the tracker live, not replay
// the pre-fix error for the remaining window.
func (c *SearchCache) InvalidateByInstance(ctx context.Context, instanceID int64) (int64, error) {
	c.bumpInstanceEpoch(instanceID)
	c.breaker.forget(instanceID)
	n, err := c.store.InvalidateByInstance(ctx, c.db, instanceID)
	if err != nil {
		return 0, err //nolint:wrapcheck // store wraps with the instance id; nothing secret to add.
	}
	return n, nil
}

// hitRatio is hits/(hits+misses), or 0 when there has been no traffic.
func hitRatio(hits, misses int64) float64 {
	total := hits + misses
	if total == 0 {
		return 0
	}
	return float64(hits) / float64(total)
}

// unixSecPtr converts an optional timestamp to an optional Unix-seconds pointer for
// the JSON stats response (nil stays nil).
func unixSecPtr(t *time.Time) *int64 {
	if t == nil {
		return nil
	}
	s := t.Unix()
	return &s
}

// decodeError wraps a cached-payload decode failure with ONLY the cache key — never
// the payload — so a malformed row can never leak a passkey-bearing link.
func decodeError(key string, err error) error {
	return fmt.Errorf("registry: decode search cache %q: %w", key, err)
}
