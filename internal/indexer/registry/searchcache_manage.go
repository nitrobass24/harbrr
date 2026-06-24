package registry

import (
	"context"
	"fmt"
	"time"
)

// SearchCacheStats is the management view of the cache: the durable row-derived
// figures plus the process-lifetime hit-ratio counters. Hits/Misses/HitRatio are
// non-persistent (they reset on restart); the rest are read from the store.
type SearchCacheStats struct {
	Entries         int64
	TotalHits       int64
	ApproxSizeBytes int64
	OldestUnixSec   *int64
	NewestUnixSec   *int64
	LastUsedUnixSec *int64

	// Process-lifetime (non-persistent) counters.
	Hits     int64
	Misses   int64
	HitRatio float64
}

// Stats returns the cache statistics: durable store figures plus the in-memory
// hit-ratio. The store error wraps nothing secret (it has no payload to leak).
func (c *SearchCache) Stats(ctx context.Context) (SearchCacheStats, error) {
	s, err := c.store.Stats(ctx, c.db)
	if err != nil {
		return SearchCacheStats{}, err //nolint:wrapcheck // store already wraps with context; no key/payload to add.
	}
	hits, misses := c.hits.Load(), c.misses.Load()
	out := SearchCacheStats{
		Entries:         s.Entries,
		TotalHits:       s.TotalHits,
		ApproxSizeBytes: s.ApproxSizeBytes,
		OldestUnixSec:   unixSecPtr(s.Oldest),
		NewestUnixSec:   unixSecPtr(s.Newest),
		LastUsedUnixSec: unixSecPtr(s.LastUsed),
		Hits:            hits,
		Misses:          misses,
		HitRatio:        hitRatio(hits, misses),
	}
	return out, nil
}

// Flush deletes every cache entry and returns the count purged. It does not reset
// the in-memory hit/miss counters (they are process-lifetime by design).
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
