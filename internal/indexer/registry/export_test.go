package registry

import (
	"time"

	"github.com/rs/zerolog"

	"github.com/autobrr/harbrr/internal/database/dbinterface"
	"github.com/autobrr/harbrr/internal/indexer/core"
)

// NewSearchCacheForTest builds a SearchCache with default keyword/rss/thin tiers and
// refresh-ahead disabled, for the external (registry_test) regression suite. It
// exists only in test builds.
func NewSearchCacheForTest(db dbinterface.Querier, clock func() time.Time) *SearchCache {
	t := cacheTuning{
		enabled:   true,
		ttl:       ttlConfig{rss: 5 * time.Minute, keyword: 30 * time.Minute, thin: 2 * time.Minute, thinThreshold: 5},
		refreshAt: 0,
	}
	return newSearchCache(db, t, clock, zerolog.Nop())
}

// WrapForTest serves a fake indexer through the cache's cache-aside path (via the
// test-only cacheProbe scaffold) so the external suite can drive a cached indexer through
// the real Torznab handler. Driver-backed paging tests instead resolve the real flattened
// adapter via reg.Indexer with WithSearchCache; this stays for fakes that are not drivers.
func WrapForTest(sc *SearchCache, inner core.Indexer, instanceID int64) core.Indexer {
	return sc.probe(inner, instanceID, nil)
}
