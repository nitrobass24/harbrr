package search

import (
	"fmt"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/loader"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/normalizer"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/selector"
)

// backfillDateHeader reproduces Jackett's dateheaders backfill (CardigannIndexer
// search loop, after the row filters): when a KEPT release has no PublishDate and
// the def sets rows.dateheaders, walk the preceding elements for the nearest one
// the dateheaders selector matches and adopt its date. It is HTML only, mirroring
// Jackett's AngleSharp-DOM traversal (the JSON/XML branches have no dateheaders).
//
// Jackett backfills only when PublishDate == DateTime.MinValue, i.e. no `date`
// field set it; a present date is never overwritten. When the walk finds no header
// and dateheaders is not optional, Jackett throws "No date header row found",
// which its per-row try/catch turns into a dropped row — so backfillDateHeader
// returns an error there and ParseResults' HTML row-skip drops the row identically.
func backfillDateHeader(def *loader.Definition, row selector.Row, rel *normalizer.Release, query Query, deps Deps, respType string) error {
	block := def.Search.Rows.DateHeaders
	if block == nil || respType == responseTypeJSON || respType == responseTypeXML {
		return nil
	}
	if rel.PublishDate != "" {
		return nil
	}

	for cand := range row.PrecedingElements() {
		value, ok := handleHeaderSelector(*block, cand, query, deps)
		if !ok {
			// Jackett swallows every handleSelector failure (no match, or a filter
			// that threw) and keeps walking preceding elements.
			continue
		}
		// Match found: Jackett breaks the walk here and runs FromUnknown on the
		// value. applyImplicitDate is the same DateTimeUtil.FromUnknown a `date`
		// field uses; a parse error is a per-row failure that drops the row.
		parsed, err := applyImplicitDate("date", value, deps)
		if err != nil {
			return fmt.Errorf("dateheaders: %w", err)
		}
		rel.PublishDate = parsed
		return nil
	}

	if block.Optional != nil && *block.Optional {
		return nil
	}
	return fmt.Errorf("dateheaders: no date-header row found: %w", selector.ErrSelectorNoMatch)
}

// handleHeaderSelector reproduces handleSelector(DateHeaders, CurRow) for one
// candidate element: extract the block's value (selector → remove →
// case/attribute/text) then run the block's filters — the same extraction a field
// gets, minus the field loop's optional/default handling (Jackett calls
// handleSelector with required=true and no defaults here). ok is false when the
// selector matches nothing on cand or any step errors, both of which Jackett's
// try/catch treats as "keep walking". variables is empty because Jackett passes
// null here — the dateheaders blocks in the corpus carry no `.Result` templates.
func handleHeaderSelector(block loader.SelectorBlock, cand selector.Row, query Query, deps Deps) (string, bool) {
	empty := map[string]string{}
	bindEval(deps, query, empty)

	value, found, err := deps.Selector.Field(cand, block)
	if err != nil || !found {
		return "", false
	}

	if deps.Filters == nil {
		return value, true
	}
	filters, err := renderFilterArgs(block.Filters, deps, query, empty)
	if err != nil {
		return "", false
	}
	value, err = deps.Filters.Apply(value, filters)
	if err != nil {
		return "", false
	}
	return value, true
}
