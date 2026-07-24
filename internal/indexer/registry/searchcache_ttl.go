package registry

import (
	"strings"
	"time"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
)

// ttlConfig holds the resolved TTL tiers for the search-results cache. rss is the
// TTL for an empty/RSS poll, keyword for a real search; thin is the short clamp
// applied when a search returns few results (so a near-empty page is not pinned
// for the full keyword TTL). thinThreshold is the inclusive result count at or
// below which the thin clamp applies.
type ttlConfig struct {
	rss           time.Duration
	keyword       time.Duration
	thin          time.Duration
	thinThreshold int
	// negative is the negative-result circuit-breaker window: after a live search to
	// an instance fails, a MISS for that instance short-circuits to the recorded error
	// for this long instead of re-driving the tracker. Zero disables the breaker (the
	// legacy behavior — every consumer re-hits a failing tracker). It is a breaker
	// window, not a cache-entry TTL, so resolveTTL never reads it.
	negative time.Duration
}

// resolveTTL picks the cache TTL for one search, mirroring registry/client.go
// resolveTimeout's "cfg override else default" shape. The base TTL is the
// instance's "cache_ttl" setting (a Go duration, e.g. "10m") when present and
// positive, else the rss tier for an empty query or the keyword tier otherwise.
// When the result count is at or below thinThreshold the TTL is clamped to
// min(base, thin) — the thin clamp can only SHORTEN, never lengthen, and it
// applies even over an explicit cache_ttl override on a thin result. Finally, an
// empty query's TTL is floored to the instance's clamped "rss_warm_interval" — see
// warmFloor.
func (c ttlConfig) resolveTTL(cfg map[string]string, q search.Query, count int) time.Duration {
	base := c.keyword
	if isEmptyQuery(q) {
		base = c.rss
	}
	if v := cfg["cache_ttl"]; v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			base = d
		}
	}
	if count <= c.thinThreshold && c.thin < base {
		base = c.thin
	}
	return warmFloor(cfg, q, base)
}

// warmFloor raises ttl to the instance's clamped "rss_warm_interval" when q is an
// empty/RSS poll and the setting is valid — never lowers it. The warm interval
// setting itself declares "keep this instance's RSS at least this fresh," so the
// same floor applies to a consumer-driven RSS write-back, not just the warmer's own
// writes: without it, a keyword-tier-shorter rss/thin TTL could expire a warmed
// entry before the next scheduled warm refreshes it, defeating the warm entirely on
// the next consumer poll in between. A keyword query is never floored — the warm
// interval only ever primes the RSS entry.
//
// No capability check runs here: stripInertWarmInterval (warmer.go) already deleted
// the setting from cfg at build time for a paging/Mode-consuming instance (no warmer
// ever refreshes those, so the floor's justification does not hold), so cfg simply
// carries no valid warmIntervalSetting for one — this stays a pure "is the setting
// present and valid" check.
func warmFloor(cfg map[string]string, q search.Query, ttl time.Duration) time.Duration {
	if !isEmptyQuery(q) {
		return ttl
	}
	if wi, ok := warmIntervalFromValue(cfg[warmIntervalSetting]); ok && wi > ttl {
		return wi
	}
	return ttl
}

// isEmptyQuery reports whether a query is an empty/RSS poll: no free-text Keywords
// AND no id/season/ep/year/music/book term. Categories are NOT a query term — an
// RSS poll carries the def's DefaultCategories yet must still classify as empty so
// it gets the rss tier, not the keyword tier. This is a dedicated predicate, not
// search.Query.isIDSearch (which omits season/ep/year and the music/book fields).
func isEmptyQuery(q search.Query) bool {
	// Every request-driving scalar field; Categories is deliberately excluded.
	// Keywords is trimmed first so a whitespace-only term classifies as empty,
	// matching the cache-key canonicalization (which trims+casefolds Keywords) —
	// otherwise "   " would key like an RSS poll but pick the keyword TTL tier.
	terms := []string{
		strings.TrimSpace(q.Keywords),
		q.IMDBID, q.TMDBID, q.TVDBID, q.TVMazeID, q.TraktID, q.DoubanID, q.RageID,
		q.Season, q.Ep, q.Year,
		q.Artist, q.Album, q.Label, q.Track, q.Author, q.BookTitle,
	}
	for _, t := range terms {
		if t != "" {
			return false
		}
	}
	return true
}
