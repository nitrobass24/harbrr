package registry

import (
	"context"
	"testing"
	"time"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
)

// TestCleanupExpired_GraceRetainsRecentlyExpired proves CleanupExpired retains an
// entry for cacheReapGrace past its expiry — long enough for the announce-source diff
// and the budget-exhausted stale serve (both of which read expired rows by design) to
// keep working across a cleanup tick — and reaps it once the grace has fully elapsed.
func TestCleanupExpired_GraceRetainsRecentlyExpired(t *testing.T) {
	t.Parallel()
	ttl := ttlConfig{rss: time.Minute, keyword: time.Minute, thin: time.Minute, thinThreshold: 100}
	sc, instID, clk := testCache(t, ttl, 0)
	ctx := context.Background()
	q := search.Query{Keywords: "x"}

	sc.storeBestEffort(ctx, instID, map[string]string{}, 0, q, "k", relSet("A"))

	// Just past the 1m TTL but well inside the 24h reap grace: a cleanup tick must not
	// purge the row yet.
	advance(clk, 2*time.Minute)
	if n, err := sc.CleanupExpired(ctx); err != nil {
		t.Fatalf("CleanupExpired: %v", err)
	} else if n != 0 {
		t.Fatalf("CleanupExpired purged %d, want 0 (still within grace)", n)
	}
	stats, err := sc.Stats(ctx)
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if stats.Entries != 1 {
		t.Fatalf("entries after early tick = %d, want 1 (grace-retained row still counts)", stats.Entries)
	}

	// Past the full grace: the next tick reaps it.
	advance(clk, cacheReapGrace+time.Minute)
	n, err := sc.CleanupExpired(ctx)
	if err != nil {
		t.Fatalf("CleanupExpired: %v", err)
	}
	if n != 1 {
		t.Fatalf("CleanupExpired purged %d, want 1 (grace elapsed)", n)
	}
	stats, err = sc.Stats(ctx)
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if stats.Entries != 0 {
		t.Fatalf("entries after grace tick = %d, want 0", stats.Entries)
	}
}
