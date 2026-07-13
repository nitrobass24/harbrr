package search

import (
	"fmt"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/loader"
)

// applyKeywordsFilters runs the definition's search.keywordsfilters over the
// joined keyword term and stores the result on the returned query, reproducing
// Jackett PerformQuery's
// variables[".Keywords"] = ApplyFilters(variables[".Query.Keywords"], Search.Keywordsfilters)
// which runs BEFORE request templating. Only the top-level .Keywords value is
// filtered — .Query.Keywords stays raw (queryMap) — and the andmatch row filter
// reads the same filtered value (templateKeywords). Filter args may carry
// template fragments, like field filters (renderFilterArgs). A definition
// without keywordsfilters is a no-op. Called at both executor entry points
// (buildRequests, ParseResults); re-applying is harmless because the filters
// always run over the raw term.
func applyKeywordsFilters(def *loader.Definition, query Query, deps Deps) (Query, error) {
	if len(def.Search.KeywordsFilters) == 0 {
		return query, nil
	}
	filters, err := renderFilterArgs(def.Search.KeywordsFilters, deps, query, nil)
	if err != nil {
		return Query{}, fmt.Errorf("keywordsfilters: %w", err)
	}
	filtered, err := deps.Filters.apply(query.keywords(), filters)
	if err != nil {
		return Query{}, fmt.Errorf("keywordsfilters: %w", err)
	}
	query.keywordsFiltered = &filtered
	return query, nil
}
