package registry

import (
	"time"

	"github.com/rs/zerolog"

	"github.com/autobrr/harbrr/internal/database/dbinterface"
	"github.com/autobrr/harbrr/internal/web/torznab"
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
	return NewSearchCache(db, t, clock, zerolog.Nop())
}

// WrapForTest exposes the unexported cache decorator to the external test package so
// it can serve a cached indexer through the real Torznab handler.
func WrapForTest(sc *SearchCache, inner torznab.Indexer, instanceID int64) torznab.Indexer {
	return sc.wrap(inner, instanceID, nil)
}
