package search

import (
	"fmt"
	"strings"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/filter"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/loader"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/normalizer"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/selector"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/template"
)

// optionalFields mirrors Jackett's OptionalFields: fields treated as optional
// even without an explicit optional flag, so a miss yields an empty value rather
// than an error.
var optionalFields = map[string]struct{}{
	"imdb": {}, "imdbid": {}, "tmdbid": {}, "rageid": {}, "tvdbid": {},
	"tvmazeid": {}, "traktid": {}, "doubanid": {}, "poster": {},
	"genre": {}, "description": {},
}

// rowState threads the growing per-row Result map and accumulated base-field map
// through the field loop. result feeds .Result.<name> template reads; base is the
// flat base-field map handed to the normalizer.
type rowState struct {
	result map[string]string
	base   map[string]string
}

// parseRow runs the field loop for one row and decides whether to keep it. It
// reproduces Jackett's per-row body: iterate Search.Fields IN DEFINITION ORDER,
// extract+default+filter each field with the selector EvalTemplate bound to the
// growing Result, accumulate into base/result, apply the row filters, then build
// the Release. keep is false when a row filter (andmatch) drops the row.
func parseRow(def *loader.Definition, row selector.Row, query Query, deps Deps) (rel *normalizer.Release, keep bool, err error) {
	state := rowState{result: map[string]string{}, base: map[string]string{}}

	for _, fe := range def.Search.Fields.Ordered() {
		if err := parseField(fe, row, query, deps, &state); err != nil {
			return nil, false, err
		}
	}

	if skip := applyRowFilters(def.Search.Rows.Filters, state.base["title"], query); skip {
		return nil, false, nil
	}

	rel, err = deps.Normalizer.Release(state.base)
	if err != nil {
		return nil, false, fmt.Errorf("normalizing row: %w", err)
	}
	return rel, true, nil
}

// parseField extracts, defaults, and filters one field, then folds it into the
// row state. The field key may carry modifiers ("title|append"); the base name
// (FieldParts[0]) is what keys .Result and the base map. The selector's
// EvalTemplate is rebound to see the Result map accumulated so far, reproducing
// Jackett's handleSelector(variables) interleaving.
func parseField(fe loader.FieldEntry, row selector.Row, query Query, deps Deps, state *rowState) error {
	name, modifiers := splitFieldKey(fe.Key)
	optional := isOptional(fe.Key, name, modifiers, fe.Block)

	bindEval(deps, query, state.result)

	value, found, err := deps.Selector.Field(row, fe.Block)
	if err != nil {
		// A genuine fault (bad selector/template/case eval) — NOT "value absent",
		// which Field reports as found=false with a nil error and which the
		// optional/default logic below handles. Propagate even for optional
		// fields so a malformed def surfaces loudly; ParseResults then drops the
		// row (HTML) or aborts (JSON), mirroring Jackett's row-level try/catch.
		return fmt.Errorf("field %q: %w", name, err)
	}

	// Jackett applies the field's filters INSIDE handleSelector, before the
	// optional/default check runs (CardigannIndexer.handleSelector). A filter that
	// reduces a non-empty value to empty must therefore be able to trigger the
	// default, so filters run first.
	if found {
		value, err = deps.Filters.Apply(value, fe.Block.Filters)
		if err != nil {
			return fmt.Errorf("field %q: %w", name, err)
		}
	}

	resolved, skip, err := resolveValue(value, found, optional, fe.Block, deps, query, state.result)
	if err != nil {
		return fmt.Errorf("field %q: %w", name, err)
	}
	if skip {
		state.result[name] = ""
		return nil
	}

	storeField(state, name, modifiers, resolved)
	return nil
}

// resolveValue applies the required/optional + default branch after extraction
// and field filtering. A required miss is a loud error; an optional miss (or a
// value the filters reduced to empty) tries the field's default template, and
// when that is also empty the field is skipped (Result[name]=nil in Jackett). The
// default is used verbatim — Jackett does NOT re-run the field filters over it.
// Returns the resolved value, whether to skip the field, or an error.
func resolveValue(value string, found, optional bool, block loader.SelectorBlock, deps Deps, query Query, result map[string]string) (string, bool, error) {
	if found && strings.TrimSpace(value) != "" {
		return value, false, nil
	}
	if !optional {
		if !found {
			return "", false, fmt.Errorf("required selector matched nothing: %w", selector.ErrSelectorNoMatch)
		}
		return value, false, nil
	}
	// optional + empty (no match, or filtered to empty): try the default template.
	def := ""
	if block.Default != nil {
		def = block.Default.String()
	}
	rendered, err := evalTemplate(deps, query, result, def)
	if err != nil {
		return "", false, err
	}
	if strings.TrimSpace(rendered) == "" {
		return "", true, nil
	}
	return rendered, false, nil
}

// storeField folds a resolved field value into the row state. The base map keys
// on the base field name; the |append modifier concatenates onto the existing
// value (title|append / description|append in the corpus). The Result map always
// records the latest value for cross-field .Result reads.
func storeField(state *rowState, name string, modifiers []string, value string) {
	if hasModifier(modifiers, "append") {
		state.base[name] += value
	} else {
		state.base[name] = value
	}
	state.result[name] = state.base[name]
}

// applyRowFilters reproduces ParseRowFilters: returns true (skip the row) when an
// andmatch filter's keywords are not all present in the title. andmatch is
// skipped entirely for ID-based searches (imdb/tmdb/...), matching Jackett.
// strdump and unknown names never skip.
func applyRowFilters(filters []loader.RowFilterBlock, title string, query Query) bool {
	for i := range filters {
		if filters[i].Name != "andmatch" {
			continue
		}
		if query.isIDSearch() {
			continue
		}
		if !filter.AndMatch(title, query.keywords()) {
			return true
		}
	}
	return false
}

// bindEval rebinds the selector's EvalTemplate seam so selector strings, case
// values, and text are evaluated against the current Result map. Called before
// each field so later fields see earlier .Result values.
func bindEval(deps Deps, query Query, result map[string]string) {
	deps.Selector.EvalTemplate = func(s string) (string, error) {
		return evalTemplate(deps, query, result, s)
	}
}

// evalTemplate evaluates one template fragment against a fresh context seeded
// with config + query + the current Result map. A fresh context per call is
// required because template.Eval mutates it.
func evalTemplate(deps Deps, query Query, result map[string]string, text string) (string, error) {
	config := withSitelink(deps.Config, deps.BaseURL)
	ctx := newContext(config, query.queryMap(), result, query.keywords(), query.Categories, deps.Clock)
	out, err := template.Eval(text, ctx)
	if err != nil {
		return "", fmt.Errorf("evaluating field template: %w", err)
	}
	return out, nil
}

// splitFieldKey splits "title|append|optional" into the base name and modifiers.
func splitFieldKey(key string) (name string, modifiers []string) {
	parts := strings.Split(key, "|")
	return parts[0], parts[1:]
}

// isOptional reports whether a field is optional: an OptionalFields member, an
// "optional" modifier, or the block's optional flag, matching Jackett.
func isOptional(key, name string, modifiers []string, block loader.SelectorBlock) bool {
	if _, ok := optionalFields[key]; ok {
		return true
	}
	if _, ok := optionalFields[name]; ok {
		return true
	}
	if hasModifier(modifiers, "optional") {
		return true
	}
	return block.Optional != nil && *block.Optional
}

// hasModifier reports whether modifiers contains name.
func hasModifier(modifiers []string, name string) bool {
	for _, m := range modifiers {
		if m == name {
			return true
		}
	}
	return false
}
