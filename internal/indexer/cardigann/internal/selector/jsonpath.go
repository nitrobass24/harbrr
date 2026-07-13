package selector

import (
	"encoding/json"
	"strconv"
	"strings"
	"time"
)

// resolveRowsArray resolves rows.selector to a JSON array and applies its
// pseudo-selector filters. The selector is a path ("$" / dotted) to an array,
// optionally followed by :has/:not/:contains conditions that Jackett applies to
// each element (JsonParseRowsSelector: SelectToken(pathBeforeFirstColon), then
// .Where(JsonParseFieldSelector(element, rest) != null)). ok is false when the
// path is absent or is not an array; the caller maps that to Jackett's "0 rows"
// vs error branch.
func resolveRowsArray(root any, selector string) ([]any, bool, error) {
	target := root
	if path := rowsPath(selector); path != "" {
		v, ok := resolvePath(root, path)
		if !ok {
			return nil, false, nil
		}
		target = v
	}

	arr, ok := target.([]any)
	if !ok {
		return nil, false, nil
	}

	remainder := rowsFilters(selector)
	if remainder == "" {
		return arr, true, nil
	}
	filtered := make([]any, 0, len(arr))
	for _, e := range arr {
		if _, ok := jsonFieldSelector(e, remainder); ok {
			filtered = append(filtered, e)
		}
	}
	return filtered, true, nil
}

// rowsPath normalizes a rows.selector into a resolvable path: "$" and "$." mean
// the root (empty path); a leading "." or "$." prefix is trimmed; a trailing
// ":..." pseudo-selector (applied by resolveRowsArray, not part of the path) is
// dropped.
func rowsPath(selector string) string {
	s := strings.TrimSpace(selector)
	if i := strings.IndexByte(s, ':'); i >= 0 {
		s = s[:i]
	}
	s = strings.TrimPrefix(s, "$")
	return trimDotPrefix(s)
}

// rowsFilters returns the pseudo-selector remainder of a rows.selector (from the
// first ':' to the end), or "" when the selector is a bare path. It is the
// per-element condition string Jackett feeds to JsonParseFieldSelector.
func rowsFilters(selector string) string {
	s := strings.TrimSpace(selector)
	if i := strings.IndexByte(s, ':'); i >= 0 {
		return s[i:]
	}
	return ""
}

// resolvePath walks a Newtonsoft-style SelectToken path over a JSON value decoded
// into Go's any (map[string]any / []any / scalars). It supports the corpus subset:
// dotted object keys and array indices written either as a dotted segment
// ("tags.0") or as Newtonsoft bracket syntax ("files[0]", "$[0].id"). A leading
// "$" or "." is the caller's responsibility to strip. ok is false on any missing
// key, out-of-range index, or type mismatch.
func resolvePath(root any, path string) (any, bool) {
	p := strings.TrimPrefix(strings.TrimSpace(path), "$")
	p = trimDotPrefix(p)
	tokens := tokenizePath(p)
	if len(tokens) == 0 {
		return root, true
	}

	cur := root
	for _, tok := range tokens {
		next, ok := descend(cur, tok)
		if !ok {
			return nil, false
		}
		cur = next
	}
	return cur, true
}

// pathToken is one resolved step of a JSON path: a map key or an array index.
type pathToken struct {
	key   string
	index int
	isIdx bool
}

// tokenizePath splits a Newtonsoft-style path into ordered key/index tokens,
// handling both dotted indices ("tags.0") and bracket indices ("files[0]",
// "[0]"). The corpus uses single-int bracket subscripts; quoted/string bracket
// keys do not appear in the snapshot, so a bracketed segment is treated as an
// index when it parses as an int and otherwise as an object key.
func tokenizePath(p string) []pathToken {
	var tokens []pathToken
	for _, seg := range strings.Split(p, ".") {
		if seg == "" {
			continue
		}
		tokens = appendSegmentTokens(tokens, seg)
	}
	return tokens
}

// appendSegmentTokens expands one dot-delimited segment, peeling any trailing
// "[N]" bracket subscripts (e.g. "files[0]" -> key "files" then index 0).
func appendSegmentTokens(tokens []pathToken, seg string) []pathToken {
	name, brackets := splitBrackets(seg)
	if name != "" {
		tokens = append(tokens, classifySegment(name))
	}
	for _, b := range brackets {
		tokens = append(tokens, classifySegment(b))
	}
	return tokens
}

// splitBrackets separates a segment's leading name from its trailing bracket
// subscripts: "files[0]" -> ("files", ["0"]); "[0]" -> ("", ["0"]); "name" ->
// ("name", nil).
func splitBrackets(seg string) (name string, subscripts []string) {
	open := strings.IndexByte(seg, '[')
	if open < 0 {
		return seg, nil
	}
	name = seg[:open]
	rest := seg[open:]
	for len(rest) > 0 && rest[0] == '[' {
		end := strings.IndexByte(rest, ']')
		if end < 0 {
			break
		}
		subscripts = append(subscripts, rest[1:end])
		rest = rest[end+1:]
	}
	return name, subscripts
}

// classifySegment turns a name/subscript string into an index token when it is an
// integer, otherwise into a key token.
func classifySegment(s string) pathToken {
	if idx, err := strconv.Atoi(s); err == nil {
		return pathToken{index: idx, isIdx: true}
	}
	return pathToken{key: s}
}

// descend resolves a single path token against cur: an index token subscripts a
// slice; a key token keys into a map.
func descend(cur any, tok pathToken) (any, bool) {
	if tok.isIdx {
		arr, ok := cur.([]any)
		if !ok || tok.index < 0 || tok.index >= len(arr) {
			return nil, false
		}
		return arr[tok.index], true
	}
	obj, ok := cur.(map[string]any)
	if !ok {
		return nil, false
	}
	v, ok := obj[tok.key]
	return v, ok
}

// canonicalString renders a JSON value the way Jackett observes it after
// SelectToken: scalars use JToken.ToString() canonical forms, an array joins its
// elements with commas (String.Join(",", JArray)), and null/object render empty.
func canonicalString(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return newtonsoftJSONString(t)
	case bool:
		if t {
			return "True"
		}
		return "False"
	case json.Number:
		return t.String()
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case []any:
		return joinArray(t)
	default:
		return ""
	}
}

// newtonsoftJSONString reproduces Newtonsoft.Json's default
// DateParseHandling.DateTime: while parsing JSON, a string VALUE that is an
// ISO-8601 date-time is converted to a .NET DateTime, so when Jackett later reads
// it as a string it gets the InvariantCulture general format ("MM/dd/yyyy
// HH:mm:ss") — fractional seconds and the zone designator dropped. Definitions
// rely on this (e.g. UNIT3D's created_at: append " +00:00" then
// dateparse "MM/dd/yyyy HH:mm:ss zzz"), so harbrr must reproduce it for JSON-feed
// parity. Go's encoding/json keeps the raw string, hence this shim.
//
// Detection matches Newtonsoft's ISO parser, which requires the "T" date/time
// separator: "2021-10-18T00:34:50.000000Z" converts, but a space-separated
// "2021-10-18 00:34:50" (e.g. DigitalCore's `added`) does NOT — it stays raw and
// its own dateparse layout handles it. Non-date strings pass through unchanged.
func newtonsoftJSONString(s string) string {
	if len(s) < 19 || s[4] != '-' || s[7] != '-' || s[10] != 'T' {
		return s
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02T15:04:05"} {
		if ts, err := time.Parse(layout, s); err == nil {
			return ts.Format("01/02/2006 15:04:05")
		}
	}
	return s
}

// joinArray renders a JSON array as Jackett does for a leaf array selection:
// String.Join(",", valueArray) over each element's canonical string.
func joinArray(arr []any) string {
	parts := make([]string, 0, len(arr))
	for _, e := range arr {
		parts = append(parts, canonicalString(e))
	}
	return strings.Join(parts, ",")
}
