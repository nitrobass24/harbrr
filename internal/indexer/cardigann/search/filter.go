package search

import (
	"fmt"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/loader"
)

// filterFunc transforms a field value given its (already []string-normalized)
// filter arguments. It is the per-op unit dispatched by apply.
type filterFunc func(value string, args []string) (string, error)

// FilterRegistry is the bounded Cardigann filter registry. It maps every filter name
// in the schema vocabulary to its .NET-equivalent implementation and chains
// them left-to-right over an extracted field value.
//
// Date-bearing filters delegate to injectable dependencies so this stage stays
// decoupled from the dateparse stage, which supplies them. Regex-bearing filters
// route through the shared .NET-aware regexadapter for RE2-vs-regexp2 selection.
// The seam is constructor injection: NewFilterRegistry requires the date
// dependencies and language up front, so there is no unwired state to defend
// against once a registry exists.
type FilterRegistry struct {
	// parseDate evaluates dateparse/timeparse: value is the extracted string,
	// layout is the .NET date layout from the filter args.
	parseDate func(value, layout string) (string, error)
	// parseRelTime evaluates timeago/reltime/fuzzytime relative-time formats.
	parseRelTime func(value string) (string, error)

	// language is the Cardigann def `language:` code, used to route the regex
	// filters (re_replace/regexp) to regexp2 for non-Latin scripts. The empty
	// value is Latin (RE2). The engine sets this per definition.
	language string

	ops map[string]filterFunc
}

// NewFilterRegistry constructs a FilterRegistry with every schema filter wired.
// The date dependencies are required at construction — there is no unwired state.
func NewFilterRegistry(
	parseDate func(value, layout string) (string, error),
	parseRelTime func(value string) (string, error),
	language string,
) *FilterRegistry {
	r := &FilterRegistry{parseDate: parseDate, parseRelTime: parseRelTime, language: language}
	r.ops = r.buildOps()
	return r
}

// buildOps assembles the name->func dispatch table. Date and rel-time entries
// close over the registry so the injected dependencies are honored at call
// time (not at construction), keeping the item-6 seam live.
func (r *FilterRegistry) buildOps() map[string]filterFunc {
	ops := map[string]filterFunc{
		"querystring":   filterQueryString,
		"regexp":        r.filterRegexp,
		"re_replace":    r.filterReReplace,
		"split":         filterSplit,
		"replace":       filterReplace,
		"trim":          filterTrim,
		"prepend":       filterPrepend,
		"append":        filterAppend,
		"tolower":       filterToLower,
		"toupper":       filterToUpper,
		"urldecode":     filterURLDecode,
		"urlencode":     filterURLEncode,
		"htmldecode":    filterHTMLDecode,
		"htmlencode":    filterHTMLEncode,
		"validfilename": filterValidFilename,
		"diacritics":    filterDiacritics,
		"jsonjoinarray": filterJSONJoinArray,
		"hexdump":       filterPassthrough,
		"strdump":       filterPassthrough,
		"validate":      filterValidate,
	}

	ops["dateparse"] = r.dateOp
	ops["timeparse"] = r.dateOp
	ops["timeago"] = r.relTimeOp
	ops["reltime"] = r.relTimeOp
	ops["fuzzytime"] = r.relTimeOp

	return ops
}

// dateOp dispatches dateparse/timeparse to the injected parseDate. The layout
// is the first filter arg (Jackett casts Filter.Args to a single string).
func (r *FilterRegistry) dateOp(value string, args []string) (string, error) {
	out, err := r.parseDate(value, firstArg(args))
	if err != nil {
		return "", fmt.Errorf("dateparse filter: %w", err)
	}
	return out, nil
}

// relTimeOp dispatches timeago/reltime/fuzzytime to the injected parseRelTime.
func (r *FilterRegistry) relTimeOp(value string, _ []string) (string, error) {
	out, err := r.parseRelTime(value)
	if err != nil {
		return "", fmt.Errorf("reltime filter: %w", err)
	}
	return out, nil
}

// apply runs the filter chain over value, threading each op's output into the
// next op's input (left-to-right), mirroring Jackett's applyFilters. An unknown
// filter name is a loud error — the value is never silently passed through.
func (r *FilterRegistry) apply(value string, filters []loader.FilterBlock) (string, error) {
	out := value
	for i, f := range filters {
		op, ok := r.ops[f.Name]
		if !ok {
			return "", fmt.Errorf("filter %d: unknown filter name %q", i, f.Name)
		}
		next, err := op(out, f.Args)
		if err != nil {
			// Error strings reference the filter NAME + arg shape only — filter
			// values/args may embed passkey URLs and must never be logged.
			return "", fmt.Errorf("filter %d (%s, %d args): %w", i, f.Name, len(f.Args), err)
		}
		out = next
	}
	return out, nil
}

// known reports whether name is a registered FIELD filter. Validating a whole
// definition requires BOTH this and rowFilterKnown (for RowsBlock.Filters) —
// field and row chains are separate vocabularies; see rowFilterKnown.
func (r *FilterRegistry) known(name string) bool {
	_, ok := r.ops[name]
	return ok
}

// firstArg returns args[0] or "" when the slice is empty, matching Jackett's
// cast of an absent Filter.Args to a null/empty string.
func firstArg(args []string) string {
	if len(args) == 0 {
		return ""
	}
	return args[0]
}
