package search

import (
	"fmt"
	"unicode"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/regexadapter"
)

// Regex filters route through regexadapter: RE2 by default (ReDoS-safe), regexp2
// on .NET-only constructs, RE2 compile-failure, or — via the FilterRegistry's Language
// field — a non-Latin def script. These are methods on FilterRegistry so the per-def
// language routing seam (set by the engine per definition) is honored at call time.

// filterReReplace implements re_replace[pattern,repl]: regex replace-all with
// .NET replacement-token semantics. Routing uses the registry Language so a
// non-Latin def reaches regexp2.
func (r *FilterRegistry) filterReReplace(value string, args []string) (string, error) {
	if len(args) < 2 {
		return "", fmt.Errorf("re_replace needs 2 args, got %d: %w", len(args), errMissingArg)
	}
	re, err := regexadapter.Compile(args[0], r.routeOptions())
	if err != nil {
		return "", fmt.Errorf("re_replace: %w", err)
	}
	out, err := re.ReplaceAllString(value, args[1])
	if err != nil {
		return "", fmt.Errorf("re_replace: %w", err)
	}
	return out, nil
}

// filterRegexp implements regexp[pattern]: match the pattern and return capture
// group 1's value. Jackett returns Match.Groups[1].Value, which is "" when the
// pattern does not match or has no group 1 — so a no-match yields "". Routing
// uses the registry Language.
func (r *FilterRegistry) filterRegexp(value string, args []string) (string, error) {
	re, err := regexadapter.Compile(firstArg(args), r.routeOptions())
	if err != nil {
		return "", fmt.Errorf("regexp: %w", err)
	}
	m, err := re.FindStringSubmatch(value)
	if err != nil {
		return "", fmt.Errorf("regexp: %w", err)
	}
	if len(m) < 2 {
		// No match, or a match with no capture group 1: Jackett returns "".
		return "", nil
	}
	return m[1], nil
}

// routeOptions builds the regexadapter routing inputs from the registry's
// per-def Language (set by the engine; "" = Latin default).
func (r *FilterRegistry) routeOptions() regexadapter.RouteOptions {
	return regexadapter.RouteOptions{Language: r.Language}
}

// isNonSpacingMark reports whether r is a Unicode non-spacing mark (category
// Mn), matching .NET's UnicodeCategory.NonSpacingMark used by the diacritics
// filter.
func isNonSpacingMark(r rune) bool {
	return unicode.Is(unicode.Mn, r)
}
