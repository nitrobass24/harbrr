package registry

import (
	"context"
	"fmt"
	"sort"
	"time"
)

// SearchCacheStats is the management view of the cache: the durable row-derived
// figures plus the hit-ratio counters. Hits/Misses/HitRatio/BreakerSuppressed survive
// a restart (persisted via counterStore); the rest are read from the store.
type SearchCacheStats struct {
	Entries         int64
	TotalHits       int64
	ApproxSizeBytes int64
	OldestUnixSec   *int64
	NewestUnixSec   *int64
	LastUsedUnixSec *int64

	// Cumulative counters, persisted across restarts (see searchcache_counters.go).
	Hits     int64
	Misses   int64
	HitRatio float64
	// BreakerSuppressed counts MISSes short-circuited by an open negative breaker —
	// tracker requests the breaker spared.
	BreakerSuppressed int64
}

// InstanceCacheStats is one instance's merged cache observability: the durable
// row-derived figures (HitsSaved/Entries/ApproxSizeBytes) plus the in-memory counters
// (persisted across restarts) and the live breaker state. HitsSaved is the headline
// "tracker requests this indexer served from cache" figure.
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

// Flush deletes every cache entry and returns the count purged. It does not reset
// the hit/miss counters (they are cumulative and monotonic, with no reset path).
func (c *SearchCache) Flush(ctx context.Context) (int64, error) {
	n, err := c.store.Flush(ctx, c.db)
	if err != nil {
		return 0, err //nolint:wrapcheck // store wraps with context; nothing secret to add.
	}
	return n, nil
}

// CleanupExpired deletes every expired entry, returning the count purged. The
// background ticker calls it.
func (c *SearchCache) CleanupExpired(ctx context.Context) (int64, error) {
	n, err := c.store.CleanupExpired(ctx, c.db, c.clock())
	if err != nil {
		return 0, err //nolint:wrapcheck // store wraps with context; nothing secret to add.
	}
	return n, nil
}

// InvalidateByInstance purges every entry for one instance (called after a config
// mutation), returning the count purged.
func (c *SearchCache) InvalidateByInstance(ctx context.Context, instanceID int64) (int64, error) {
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
