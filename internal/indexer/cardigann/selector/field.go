package selector

import (
	"sort"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/loader"
)

// caseEntry is a single switch arm of a SelectorBlock.Case.
type caseEntry struct {
	key   string
	value loader.Scalar
}

// orderedCases yields the case arms in a deterministic order. Jackett iterates
// Selector.Case in definition order, but the loader decodes it into a Go map,
// which is unordered. We restore a stable, parity-safe order: non-wildcard keys
// sorted, then the "*" catch-all last. This matches every real definition,
// where "*" is authored as the final arm precisely because it always matches;
// putting it last preserves first-specific-then-catch-all semantics regardless
// of map iteration order.
func orderedCases(cases map[string]loader.Scalar) []caseEntry {
	keys := make([]string, 0, len(cases))
	hasStar := false
	for k := range cases {
		if k == "*" {
			hasStar = true
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)

	out := make([]caseEntry, 0, len(cases))
	for _, k := range keys {
		out = append(out, caseEntry{key: k, value: cases[k]})
	}
	if hasStar {
		out = append(out, caseEntry{key: "*", value: cases["*"]})
	}
	return out
}
