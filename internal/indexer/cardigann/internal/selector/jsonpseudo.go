package selector

import "strings"

// jsonFilter is one parsed JSON pseudo-selector condition (:has/:not/:contains).
type jsonFilter struct {
	op  string
	key string
}

// parseJSONSelector splits a JSON selector into its path (everything before the
// first ':') and its ordered pseudo-selector filters, reproducing Jackett's
// _JsonSelectorRegex — \:(?<filter>.+?)\((?<key>.+?)\)(?=:|\z) — over the string.
// A filter's key extends to the first ')' that is followed by ':' or the end of
// the string (the regex's lookahead), so a NESTED filter such as
// "name:contains(1080)" is captured whole rather than split at its inner ')'.
func parseJSONSelector(sel string) (path string, filters []jsonFilter) {
	colon := strings.IndexByte(sel, ':')
	if colon < 0 {
		return sel, nil
	}
	path = sel[:colon]

	rest := sel[colon:]
	for len(rest) > 0 && rest[0] == ':' {
		open := strings.IndexByte(rest, '(')
		if open < 0 {
			break
		}
		end := closingParen(rest, open+1)
		if end < 0 {
			break
		}
		filters = append(filters, jsonFilter{op: rest[1:open], key: rest[open+1 : end]})
		rest = rest[end+1:]
	}
	return path, filters
}

// closingParen returns the index of the first ')' at or after from that is
// followed by ':' or the end of s, matching the lookahead in Jackett's
// _JsonSelectorRegex. It returns -1 when there is no such ')'.
func closingParen(s string, from int) int {
	for i := from; i < len(s); i++ {
		if s[i] == ')' && (i+1 == len(s) || s[i+1] == ':') {
			return i
		}
	}
	return -1
}

// hasJSONFilters reports whether a selector carries any pseudo-selector filter
// (Jackett's innerMatch.Success test on a :has/:not key).
func hasJSONFilters(sel string) bool {
	_, filters := parseJSONSelector(sel)
	return len(filters) > 0
}

// jsonFieldSelector reproduces Jackett JsonParseFieldSelector: resolve the
// selector's path against node, then apply each :has/:not/:contains condition.
// It returns the resolved PATH (for the caller to read the value) and whether
// node matched. A node that fails any condition — or whose path is absent — does
// not match. An empty path means "node itself" (a selector that begins with a
// filter), matching Jackett's empty Split(':')[0].
func jsonFieldSelector(node any, sel string) (string, bool) {
	path, filters := parseJSONSelector(sel)

	parent := node
	if path != "" {
		v, ok := resolvePath(node, path)
		if !ok {
			return "", false
		}
		parent = v
	}

	for _, f := range filters {
		if !applyJSONFilter(parent, f) {
			return "", false
		}
	}
	return path, true
}

// applyJSONFilter evaluates one condition against parent. Unknown filters are
// ignored (Jackett logs and continues), so they never drop a row.
func applyJSONFilter(parent any, f jsonFilter) bool {
	switch f.op {
	case "has":
		return jsonKeyPresent(parent, f.key)
	case "not":
		return !jsonKeyPresent(parent, f.key)
	case "contains":
		// Jackett tests parsedObject.ToString().Contains(key); the corpus only
		// uses :contains on string/number leaves, which canonicalString renders
		// identically. (A whole-object :contains, which Newtonsoft would
		// serialize, does not appear in the snapshot.)
		return strings.Contains(canonicalString(parent), f.key)
	default:
		return true
	}
}

// jsonKeyPresent reports whether key resolves under parent. A key that is itself
// a nested pseudo-selector is evaluated recursively (Jackett's innerMatch
// branch); otherwise it is a plain path-existence check. A present-but-null key
// counts as present, matching SelectToken (which returns a non-null JValue).
func jsonKeyPresent(parent any, key string) bool {
	if hasJSONFilters(key) {
		_, ok := jsonFieldSelector(parent, key)
		return ok
	}
	_, ok := resolvePath(parent, key)
	return ok
}
