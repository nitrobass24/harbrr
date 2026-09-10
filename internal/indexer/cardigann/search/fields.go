package search

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/internal/selector"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/internal/template"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/loader"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/normalizer"
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
// extract+default+filter each field with an eval func bound to the growing
// Result, accumulate into base/result, apply the row filters, then build the
// Release. keep is false when a row filter (andmatch) drops the row.
func parseRow(def *loader.Definition, sel *selector.Engine, row selector.Row, query Query, deps Deps) (rel *normalizer.Release, keep bool, err error) {
	state := rowState{result: map[string]string{}, base: map[string]string{}}

	for _, fe := range def.Search.Fields.Ordered() {
		if err := parseField(fe, sel, row, query, deps, &state); err != nil {
			// The row matched but this field did not resolve — the other half of
			// the "no rows" / "rows but no fields" split a capture reports. The
			// FIRST failing field wins (withMiss never overwrites).
			return nil, false, withMiss(err, SelectorMiss{
				Kind:     MissFields,
				Selector: fe.Value.Selector,
				Path:     "/search/fields/" + fe.Key,
			})
		}
	}

	if skip := applyRowFilters(def.Search.Rows.Filters, state.base["title"], query, deps); skip {
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
// (FieldParts[0]) is what keys .Result and the base map.
//
// Jackett wraps EACH field in its own try/catch (CardigannIndexer.ParseFields,
// both the HTML and JSON loops): on any exception it sets .Result.<field> to
// null when absent and, for an optional field, `continue`s — the row survives
// with the field unset and the default is NOT applied. Only a required field's
// exception reaches the row-level catch, which drops the row (HTML) or aborts
// the parse (JSON). resolveField's error is therefore swallowed here for
// optional fields and propagated verbatim for required ones.
func parseField(fe loader.Entry[loader.SelectorBlock], sel *selector.Engine, row selector.Row, query Query, deps Deps, state *rowState) error {
	name, modifiers := splitFieldKey(fe.Key)
	optional := isOptional(fe.Key, name, modifiers, fe.Value)

	resolved, skip, err := resolveField(fe.Value, name, optional, sel, row, query, deps, state.result)
	if err != nil {
		if optional {
			if _, ok := state.result[name]; !ok {
				state.result[name] = ""
			}
			return nil
		}
		return fmt.Errorf("field %q: %w", name, err)
	}
	if skip {
		state.result[name] = ""
		return nil
	}

	storeField(state, name, modifiers, resolved)
	return nil
}

// resolveField runs the extract → filter → default → implicit-date chain for one
// field and returns the resolved value, or skip=true when an optional field
// resolved to nothing. A fresh eval closure bound to the Result map accumulated
// so far is built and passed into this call's Field lookup, reproducing Jackett's
// handleSelector(variables) interleaving without mutating any shared state.
// Every error is returned as-is; parseField decides what it means for the row.
func resolveField(block loader.SelectorBlock, name string, optional bool, sel *selector.Engine, row selector.Row, query Query, deps Deps, result map[string]string) (string, bool, error) {
	eval := bindEval(deps, query, result)

	// A genuine fault (bad selector/template/case eval) — NOT "value absent",
	// which Field reports as found=false with a nil error and which the
	// optional/default logic below handles.
	value, found, err := sel.Field(row, block, eval)
	if err != nil {
		return "", false, fmt.Errorf("extracting: %w", err)
	}

	// Jackett applies the field's filters INSIDE handleSelector, before the
	// optional/default check runs (CardigannIndexer.handleSelector). A filter that
	// reduces a non-empty value to empty must therefore be able to trigger the
	// default, so filters run first.
	if found {
		filters, ferr := renderFilterArgs(block.Filters, deps, query, result)
		if ferr != nil {
			return "", false, ferr
		}
		value, err = deps.Filters.apply(value, filters)
		if err != nil {
			return "", false, err
		}
	}

	resolved, skip, err := resolveValue(value, found, optional, block, deps, query, result)
	if err != nil || skip {
		return "", skip, err
	}

	resolved, err = applyImplicitDate(name, resolved, optional, deps)
	return resolved, false, err
}

// applyImplicitDate reproduces Jackett ParseFields' case "date": the resolved
// date value (post-filters, post-default) is ALWAYS run through DateTimeUtil.
// FromUnknown before it becomes PublishDate and .Result.date. harbrr's
// ParseRelTime is the FromUnknown subset (ISO/unix/relative/named-day) and emits
// canonical RFC3339; Jackett emits RFC1123Z, so goldens hold the same instant in
// harbrr's canonical form (see parity/testdata/README.md). An unparseable date —
// including an EMPTY one, which FromUnknown("") rejects too — is a loud field
// error, which ParseResults turns into a row skip (HTML) or abort (JSON), exactly
// as Jackett's thrown exception does. Only an OPTIONAL date may be empty: Jackett
// never reaches case "date" for it (the field is skipped, and the dateheaders
// backfill may still supply the date), so it is a silent no-op here.
func applyImplicitDate(name, value string, optional bool, deps Deps) (string, error) {
	if name != "date" {
		return value, nil
	}
	if strings.TrimSpace(value) == "" {
		if optional {
			return value, nil
		}
		return "", errors.New("date field resolved to empty")
	}
	parsed, err := deps.Filters.parseRelTime(value)
	if err != nil {
		return "", fmt.Errorf("parsing date field: %w", err)
	}
	return parsed, nil
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
	if slices.Contains(modifiers, "append") {
		state.base[name] += value
	} else {
		state.base[name] = value
	}
	state.result[name] = state.base[name]
}

// applyRowFilters reproduces ParseRowFilters: returns true (skip the row) when an
// andmatch filter's keywords are not all present in the title. andmatch is
// skipped entirely for ID-based searches (imdb/tmdb/...), matching Jackett. The
// keywords are the keywordsfilters-FILTERED term (Jackett's andmatch reads the
// .Keywords variable, set after Keywordsfilters ran). strdump and unknown names
// never skip.
func applyRowFilters(filters []loader.RowFilterBlock, title string, query Query, deps Deps) bool {
	for i := range filters {
		if filters[i].Name != "andmatch" {
			continue
		}
		if query.isIDSearch() {
			continue
		}
		if !andMatch(title, query.templateKeywords(), deps.FoldAndMatchPunctuation) {
			return true
		}
	}
	return false
}

// bindEval builds an eval closure over the current Result map so selector
// strings, case values, and text are evaluated against it. Called before each
// field so later fields see earlier .Result values; the closure is passed
// directly into that field's Field call rather than mutating any shared state.
func bindEval(deps Deps, query Query, result map[string]string) selector.EvalFunc {
	return func(s string) (string, error) {
		return evalTemplate(deps, query, result, s)
	}
}

// evalTemplate evaluates one template fragment against a fresh context seeded
// with config + query + the current Result map. See template.NewSeeded for the
// fresh-context-per-call invariant.
func evalTemplate(deps Deps, query Query, result map[string]string, text string) (string, error) {
	params := requestParams(query, deps)
	params.Result = result
	ctx := template.NewSeeded(params)
	out, err := template.Eval(text, ctx)
	if err != nil {
		return "", fmt.Errorf("evaluating field template: %w", err)
	}
	return out, nil
}

// renderRowsSelector evaluates the row selector's template fragment before it is
// compiled as CSS/JSONPath. Jackett applies Go templates to the rows selector too —
// e.g. HD-Space's `... tr{{ if .Config.freeleech }}:has(img[src="gold/gold.png"]){{ end }}`
// — so the raw template would otherwise be handed to the selector compiler and fail.
// It is evaluated against config + query with NO row Result yet (no row exists at
// split time). Returns the block unchanged when the selector carries no template.
func renderRowsSelector(block loader.RowsBlock, query Query, deps Deps) (loader.RowsBlock, error) {
	if block.Selector == "" || !strings.Contains(block.Selector, "{{") {
		return block, nil
	}
	rendered, err := evalTemplate(deps, query, map[string]string{}, block.Selector)
	if err != nil {
		return block, fmt.Errorf("rendering rows selector: %w", err)
	}
	block.Selector = rendered
	return block, nil
}

// renderFilterArgs template-evaluates any filter argument that carries a Go-template
// fragment before the filter runs, reproducing Jackett's applyGoTemplateText on
// filter args (CardigannIndexer.applyFilters). Several defs guard a filter value on a
// setting — a re_replace replacement `{{ if .Config.stripcyrillic }}{{ else }}$1{{ end }}`
// or an append `{{ if .Config.addrussiantotitle }} RUS{{ end }}` (rutor and the
// Russian-tracker family). An arg with no `{{` is returned untouched, so a filter's
// regex PATTERN (which never contains `{{`) is unaffected. The def's blocks are
// copied, never mutated.
func renderFilterArgs(filters []loader.FilterBlock, deps Deps, query Query, result map[string]string) ([]loader.FilterBlock, error) {
	out := make([]loader.FilterBlock, len(filters))
	for i, f := range filters {
		out[i] = f
		if !slices.ContainsFunc(f.Args, func(a string) bool { return strings.Contains(a, "{{") }) {
			continue
		}
		rendered := make([]string, len(f.Args))
		for j, a := range f.Args {
			if !strings.Contains(a, "{{") {
				rendered[j] = a
				continue
			}
			r, err := evalTemplate(deps, query, result, a)
			if err != nil {
				return nil, fmt.Errorf("rendering %q filter arg: %w", f.Name, err)
			}
			rendered[j] = r
		}
		out[i].Args = rendered
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
	if slices.Contains(modifiers, "optional") {
		return true
	}
	return block.Optional != nil && *block.Optional
}
