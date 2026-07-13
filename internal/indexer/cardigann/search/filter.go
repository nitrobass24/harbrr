package search

import (
	"errors"
	"fmt"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/loader"
)

// errDateUnwired is returned by the default date dependencies before the engine
// injects the real implementations. Until injection, the registry knows the date
// filter names but cannot evaluate them.
var errDateUnwired = errors.New("date filters require the dateparse stage")

// filterFunc transforms a field value given its (already []string-normalized)
// filter arguments. It is the per-op unit dispatched by Apply.
type filterFunc func(value string, args []string) (string, error)

// FilterRegistry is the bounded Cardigann filter registry. It maps every filter name
// in the schema vocabulary to its .NET-equivalent implementation and chains
// them left-to-right over an extracted field value.
//
// Date-bearing filters delegate to injectable dependencies so this stage stays
// decoupled from the dateparse stage, which supplies them. Regex-bearing filters
// route through the shared .NET-aware regexadapter for RE2-vs-regexp2 selection.
type FilterRegistry struct {
	// ParseDate evaluates dateparse/timeparse: value is the extracted string,
	// layout is the .NET date layout from the filter args. Defaults to a
	// function returning errDateUnwired; the engine injects the real parser.
	ParseDate func(value, layout string) (string, error)
	// ParseRelTime evaluates timeago/reltime/fuzzytime relative-time formats.
	// Defaults to a function returning errDateUnwired; the engine injects it.
	ParseRelTime func(value string) (string, error)

	// Language is the Cardigann def `language:` code, used to route the regex
	// filters (re_replace/regexp) to regexp2 for non-Latin scripts. The empty
	// default is Latin (RE2). The engine sets this per definition.
	Language string

	ops map[string]filterFunc
}

// NewFilterRegistry constructs a FilterRegistry with every schema filter wired. Regex
// filters use RE2 inline; date dependencies default to errDateUnwired so the
// injection seam is explicit and never silently passes a value through.
func NewFilterRegistry() *FilterRegistry {
	r := &FilterRegistry{
		ParseDate: func(string, string) (string, error) {
			return "", errDateUnwired
		},
		ParseRelTime: func(string) (string, error) {
			return "", errDateUnwired
		},
	}
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

// dateOp dispatches dateparse/timeparse to the injected ParseDate. The layout
// is the first filter arg (Jackett casts Filter.Args to a single string). A nil
// dependency (a caller reassigned the seam to nil) surfaces the package's loud
// errDateUnwired rather than panicking on a nil call.
func (r *FilterRegistry) dateOp(value string, args []string) (string, error) {
	if r.ParseDate == nil {
		return "", fmt.Errorf("dateparse filter: %w", errDateUnwired)
	}
	out, err := r.ParseDate(value, firstArg(args))
	if err != nil {
		return "", fmt.Errorf("dateparse filter: %w", err)
	}
	return out, nil
}

// relTimeOp dispatches timeago/reltime/fuzzytime to the injected ParseRelTime. A
// nil dependency surfaces errDateUnwired rather than panicking on a nil call.
func (r *FilterRegistry) relTimeOp(value string, _ []string) (string, error) {
	if r.ParseRelTime == nil {
		return "", fmt.Errorf("reltime filter: %w", errDateUnwired)
	}
	out, err := r.ParseRelTime(value)
	if err != nil {
		return "", fmt.Errorf("reltime filter: %w", err)
	}
	return out, nil
}

// Apply runs the filter chain over value, threading each op's output into the
// next op's input (left-to-right), mirroring Jackett's applyFilters. An unknown
// filter name is a loud error — the value is never silently passed through.
func (r *FilterRegistry) Apply(value string, filters []loader.FilterBlock) (string, error) {
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

// Known reports whether name is a registered FIELD filter. Validating a whole
// definition requires BOTH this and rowFilterKnown (for RowsBlock.Filters) —
// field and row chains are separate vocabularies; see rowFilterKnown.
func (r *FilterRegistry) Known(name string) bool {
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
