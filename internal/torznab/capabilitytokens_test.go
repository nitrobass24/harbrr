package torznab

import (
	"reflect"
	"testing"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/mapper"
)

// TestCapabilityTokens covers flattening advertised search modes into qui's flat
// token list: each available mode plus "<mode>-q" and "<mode>-<param>" for every
// supported param, with tv-search's imdbid gated on AllowTVSearchIMDB.
func TestCapabilityTokens(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		caps *mapper.Capabilities
		want []string
	}{
		{
			"search + tv + movie, imdb gate off",
			&mapper.Capabilities{Modes: map[string][]string{
				mapper.ModeSearch:      {"q"},
				mapper.ModeTVSearch:    {"q", "season", "ep"},
				mapper.ModeMovieSearch: {"q", "imdbid"},
			}},
			[]string{
				"search", "search-q",
				"tv-search", "tv-search-q", "tv-search-season", "tv-search-ep",
				"movie-search", "movie-search-q", "movie-search-imdbid",
			},
		},
		{
			"tv-search imdbid gated on AllowTVSearchIMDB",
			&mapper.Capabilities{
				Modes:             map[string][]string{mapper.ModeTVSearch: {"q", "season"}},
				AllowTVSearchIMDB: true,
			},
			// search is always available; tv-search gains imdbid from the flag.
			[]string{
				"search", "search-q",
				"tv-search", "tv-search-q", "tv-search-season", "tv-search-imdbid",
			},
		},
		{
			"search only (no declared modes)",
			&mapper.Capabilities{Modes: map[string][]string{}},
			[]string{"search", "search-q"},
		},
		{"nil caps", nil, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := CapabilityTokens(tt.caps); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("CapabilityTokens =\n  %v\nwant\n  %v", got, tt.want)
			}
		})
	}
}
