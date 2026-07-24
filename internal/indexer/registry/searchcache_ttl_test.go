package registry

import (
	"context"
	"testing"
	"time"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
)

func testTTLConfig() ttlConfig {
	return ttlConfig{
		rss:           5 * time.Minute,
		keyword:       30 * time.Minute,
		thin:          2 * time.Minute,
		thinThreshold: 5,
	}
}

func TestResolveTTL(t *testing.T) {
	t.Parallel()

	cfg := testTTLConfig()
	emptyQ := search.Query{Categories: []string{"5000"}} // RSS poll: cats only.
	kwQ := search.Query{Keywords: "the matrix"}
	idQ := search.Query{IMDBID: "tt123"}

	tests := []struct {
		name    string
		setting map[string]string
		q       search.Query
		count   int
		want    time.Duration
	}{
		{name: "empty query => rss", q: emptyQ, count: 100, want: 5 * time.Minute},
		{name: "keyword query => keyword", q: kwQ, count: 100, want: 30 * time.Minute},
		{name: "id query => keyword", q: idQ, count: 100, want: 30 * time.Minute},
		{name: "categories alone still empty", q: search.Query{Categories: []string{"1", "2"}}, count: 100, want: 5 * time.Minute},
		// A whitespace-only keyword canonicalizes to empty in the cache key, so it
		// must pick the rss tier too (consistency with the key path).
		{name: "whitespace-only keyword => rss", q: search.Query{Keywords: "   "}, count: 100, want: 5 * time.Minute},

		{name: "override beats default", setting: map[string]string{"cache_ttl": "10m"}, q: kwQ, count: 100, want: 10 * time.Minute},
		{name: "override on empty query", setting: map[string]string{"cache_ttl": "1h"}, q: emptyQ, count: 100, want: time.Hour},
		{name: "invalid override falls back", setting: map[string]string{"cache_ttl": "nope"}, q: kwQ, count: 100, want: 30 * time.Minute},
		{name: "zero override falls back", setting: map[string]string{"cache_ttl": "0s"}, q: kwQ, count: 100, want: 30 * time.Minute},
		{name: "negative override falls back", setting: map[string]string{"cache_ttl": "-5m"}, q: kwQ, count: 100, want: 30 * time.Minute},

		{name: "thin clamp at threshold", q: kwQ, count: 5, want: 2 * time.Minute},
		{name: "thin clamp below threshold", q: kwQ, count: 3, want: 2 * time.Minute},
		{name: "zero results => thin", q: kwQ, count: 0, want: 2 * time.Minute},
		{name: "above threshold => no clamp", q: kwQ, count: 6, want: 30 * time.Minute},

		// Thin clamp only shortens: an rss base (5m) is shorter than thin (2m)? no,
		// thin is 2m < 5m so a thin rss poll clamps to 2m.
		{name: "thin clamp on rss base", q: emptyQ, count: 1, want: 2 * time.Minute},

		// Override cannot exceed the thin clamp on a thin result.
		{name: "override clamped by thin", setting: map[string]string{"cache_ttl": "1h"}, q: kwQ, count: 2, want: 2 * time.Minute},
		// A short override below thin stays (clamp only shortens, override already shorter).
		{name: "short override below thin not lengthened", setting: map[string]string{"cache_ttl": "30s"}, q: kwQ, count: 2, want: 30 * time.Second},

		// warmFloor (#341): an empty query with a valid rss_warm_interval floors the
		// rss-tier TTL up to the clamped interval (5m rss base < 15m warm interval).
		{name: "empty query floored to warm interval", setting: map[string]string{"rss_warm_interval": "15m"}, q: emptyQ, count: 100, want: 15 * time.Minute},
		// A keyword query is never floored, even with a valid warm interval configured.
		{name: "keyword query unaffected by warm interval", setting: map[string]string{"rss_warm_interval": "15m"}, q: kwQ, count: 100, want: 30 * time.Minute},
		// Invalid/absent warm interval settings are no-ops.
		{name: "invalid warm interval unaffected", setting: map[string]string{"rss_warm_interval": "nope"}, q: emptyQ, count: 100, want: 5 * time.Minute},
		{name: "absent warm interval unaffected", q: emptyQ, count: 100, want: 5 * time.Minute},
		// The floor clamps the raw setting into [warmMinInterval, warmMaxInterval]
		// (10m,120m) exactly like warmInterval does, before flooring.
		{name: "warm interval below floor clamps up then floors", setting: map[string]string{"rss_warm_interval": "1m"}, q: emptyQ, count: 100, want: warmMinInterval},
		// An explicit cache_ttl longer than the warm interval is never shortened by
		// the floor — the floor only raises.
		{name: "explicit longer cache_ttl not shortened by floor", setting: map[string]string{"cache_ttl": "1h", "rss_warm_interval": "15m"}, q: emptyQ, count: 100, want: time.Hour},
		// A thin empty-query result is still floored to the warm interval AFTER the
		// thin clamp shortens it — the floor runs last and can re-raise a thin-clamped
		// rss base past the warm interval.
		{name: "thin empty query still floored to warm interval", setting: map[string]string{"rss_warm_interval": "15m"}, q: emptyQ, count: 1, want: 15 * time.Minute},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := cfg.resolveTTL(tt.setting, tt.q, tt.count)
			if got != tt.want {
				t.Fatalf("resolveTTL(%v, count=%d) = %s, want %s", tt.setting, tt.count, got, tt.want)
			}
		})
	}
}

func TestIsEmptyQuery(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		q    search.Query
		want bool
	}{
		{name: "zero", q: search.Query{}, want: true},
		{name: "categories only", q: search.Query{Categories: []string{"5000"}}, want: true},
		{name: "whitespace-only keywords", q: search.Query{Keywords: "   "}, want: true},
		{name: "keywords", q: search.Query{Keywords: "x"}, want: false},
		{name: "imdbid", q: search.Query{IMDBID: "tt1"}, want: false},
		{name: "tmdbid", q: search.Query{TMDBID: "1"}, want: false},
		{name: "tvdbid", q: search.Query{TVDBID: "1"}, want: false},
		{name: "tvmazeid", q: search.Query{TVMazeID: "1"}, want: false},
		{name: "traktid", q: search.Query{TraktID: "1"}, want: false},
		{name: "doubanid", q: search.Query{DoubanID: "1"}, want: false},
		{name: "rageid", q: search.Query{RageID: "1"}, want: false},
		{name: "season", q: search.Query{Season: "1"}, want: false},
		{name: "ep", q: search.Query{Ep: "1"}, want: false},
		{name: "year", q: search.Query{Year: "2024"}, want: false},
		{name: "artist", q: search.Query{Artist: "x"}, want: false},
		{name: "album", q: search.Query{Album: "x"}, want: false},
		{name: "label", q: search.Query{Label: "x"}, want: false},
		{name: "track", q: search.Query{Track: "x"}, want: false},
		{name: "author", q: search.Query{Author: "x"}, want: false},
		{name: "booktitle", q: search.Query{BookTitle: "x"}, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := isEmptyQuery(tt.q); got != tt.want {
				t.Fatalf("isEmptyQuery = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestWarmSkippedInstanceRSSTTLNotFloored is the stripInertWarmInterval regression
// (#341 follow-up finding): a driver warmOne would skip (paging or Mode-consuming)
// must NOT have its RSS TTL floored by rss_warm_interval — no warmer ever refreshes
// its cache key, so the floor would just pin stale data longer for zero freshness
// benefit. An eligible driver (neither capability) keeps the floor. Drives the real
// serve path (sc.probe + cache.search, not resolveTTL directly) so it proves
// stripInertWarmInterval actually ran at probe/build time, not just that resolveTTL's
// math is right in isolation.
func TestWarmSkippedInstanceRSSTTLNotFloored(t *testing.T) {
	t.Parallel()
	// 6 releases: above keywordTTL's thinThreshold (5), so the thin clamp never
	// interferes and the rss tier (5m) is the TTL a stripped instance actually gets.
	fixture := relSet("a1", "a2", "a3", "a4", "a5", "a6")

	tests := []struct {
		name         string
		pages        bool
		consumesMode bool
		wantHit      bool // true = floor still applied (served from cache past the rss tier)
	}{
		{name: "paging driver: not floored, expires at rss tier", pages: true, wantHit: false},
		{name: "mode-consuming driver: not floored, expires at rss tier", consumesMode: true, wantHit: false},
		{name: "eligible driver: still floored, survives past rss tier", wantHit: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			sc, instID, clk := testCache(t, keywordTTL, 0) // rss tier = 5m
			inner := &fakeInner{releases: fixture, pages: tt.pages, consumesMode: tt.consumesMode}
			cfg := map[string]string{warmIntervalSetting: "15m"} // would floor rss(5m) up to 15m
			idx := sc.probe(inner, instID, cfg)
			ctx := context.Background()
			q := search.Query{} // empty/RSS poll

			if _, err := idx.Search(ctx, q); err != nil {
				t.Fatalf("first search: %v", err)
			}
			// Past the un-floored rss tier (5m) but before the would-be floor (15m).
			advance(clk, 6*time.Minute)
			if _, err := idx.Search(ctx, q); err != nil {
				t.Fatalf("second search: %v", err)
			}

			gotHit := inner.callCount() == 1
			if gotHit != tt.wantHit {
				t.Fatalf("inner called %d times at t=6m (rss tier=5m, would-be floor=15m): HIT=%v, want HIT=%v",
					inner.callCount(), gotHit, tt.wantHit)
			}
		})
	}
}
