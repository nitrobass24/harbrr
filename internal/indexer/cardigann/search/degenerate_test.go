package search

import (
	"testing"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/loader"
)

// asciiOnlyFilters is the filter almost every degenerate case in the wild comes
// from: the `[^a-zA-Z0-9 ]+` strip a large share of definitions declare in
// search.keywordsfilters, which deletes a non-Latin term outright.
var asciiOnlyFilters = []loader.FilterBlock{
	{Name: "re_replace", Args: loader.FilterArgs{`[^a-zA-Z0-9 ]+`, ""}},
	{Name: "trim"},
}

// TestDegenerateQuery pins the residual classification (autobrr/harbrr#394): a
// query is gated only when the DEFINITION'S OWN keywordsfilters turned a term that
// carried signal into one that carries none.
func TestDegenerateQuery(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		filters []loader.FilterBlock
		query   Query
		want    bool
	}{
		{
			name:    "CJK title stripped to the year is degenerate",
			filters: asciiOnlyFilters,
			query:   Query{Keywords: "君の名は", Year: "2016"},
			want:    true,
		},
		{
			name:    "CJK title stripped to nothing is degenerate",
			filters: asciiOnlyFilters,
			query:   Query{Keywords: "君の名は"},
			want:    true,
		},
		{
			name:    "Cyrillic title stripped to punctuation only is degenerate",
			filters: []loader.FilterBlock{{Name: "re_replace", Args: loader.FilterArgs{`[^a-zA-Z0-9]+`, "."}}},
			query:   Query{Keywords: "Ирония судьбы"},
			want:    true,
		},
		{
			name:    "Latin title survives the same filters",
			filters: asciiOnlyFilters,
			query:   Query{Keywords: "Big Buck Bunny", Year: "2008"},
			want:    false,
		},
		{
			name:    "mixed script keeps its Latin half",
			filters: asciiOnlyFilters,
			query:   Query{Keywords: "君の名は Your Name"},
			want:    false,
		},
		{
			name:    "an RSS poll has no term to degrade",
			filters: asciiOnlyFilters,
			query:   Query{},
			want:    false,
		},
		{
			name:    "a year-only search is the operator's own query, not our doing",
			filters: asciiOnlyFilters,
			query:   Query{Keywords: "1917"},
			want:    false,
		},
		{
			name:    "an id search is driven by the id, never gated",
			filters: asciiOnlyFilters,
			query:   Query{Keywords: "君の名は", IMDBID: "tt0000001"},
			want:    false,
		},
		{
			name:    "a definition with no keywordsfilters never degrades a term",
			filters: nil,
			query:   Query{Keywords: "君の名は"},
			want:    false,
		},
		{
			name:    "episode token survives, so a stripped CJK series is still answerable",
			filters: asciiOnlyFilters,
			query:   Query{Keywords: "君の名は", Season: "1", Ep: "2"},
			want:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			def := &loader.Definition{Search: loader.Search{KeywordsFilters: tt.filters}}
			deps := Deps{Filters: NewFilterRegistry(stubDateParse, stubRelTime, "")}
			if got := DegenerateQuery(def, tt.query, deps); got != tt.want {
				t.Errorf("DegenerateQuery = %v, want %v", got, tt.want)
			}
		})
	}
}
