package registry

import (
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
