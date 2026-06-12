package selector

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// resolveRowsArray resolves rows.selector to a JSON array. The selector is "$"
// (the root, which must itself be the array) or a dotted/indexed path to an
// array. ok is false when the path is absent or does not resolve to an array;
// the caller maps that to Jackett's "0 rows" vs error branch. Jackett strips a
// trailing ":filter" before SelectToken; we accept the bare path subset the
// corpus uses (no JSON filters on rows.selector appear in the snapshot).
func resolveRowsArray(root any, selector string) ([]any, bool, error) {
	path := rowsPath(selector)

	target := root
	if path != "" {
		v, ok, err := resolvePath(root, path)
		if err != nil {
			return nil, false, err
		}
		if !ok {
			return nil, false, nil
		}
		target = v
	}

	arr, ok := target.([]any)
	if !ok {
		return nil, false, nil
	}
	return arr, true, nil
}

// rowsPath normalizes a rows.selector into a resolvable path: "$" and "$." mean
// the root (empty path); a leading "." or "$." prefix is trimmed; a trailing
// ":..." JSON filter (rare/absent in the snapshot) is dropped.
func rowsPath(selector string) string {
	s := strings.TrimSpace(selector)
	if i := strings.IndexByte(s, ':'); i >= 0 {
		s = s[:i]
	}
	s = strings.TrimPrefix(s, "$")
	return trimDotPrefix(s)
}

// resolvePath walks a Newtonsoft-style SelectToken path over a JSON value decoded
// into Go's any (map[string]any / []any / scalars). It supports the corpus subset:
// dotted object keys and array indices written either as a dotted segment
// ("tags.0") or as Newtonsoft bracket syntax ("files[0]", "$[0].id"). A leading
// "$" or "." is the caller's responsibility to strip. ok is false on any missing
// key, out-of-range index, or type mismatch.
func resolvePath(root any, path string) (any, bool, error) {
	p := strings.TrimPrefix(strings.TrimSpace(path), "$")
	p = trimDotPrefix(p)
	tokens, err := tokenizePath(p)
	if err != nil {
		return nil, false, err
	}
	if len(tokens) == 0 {
		return root, true, nil
	}

	cur := root
	for _, tok := range tokens {
		next, ok := descend(cur, tok)
		if !ok {
			return nil, false, nil
		}
		cur = next
	}
	return cur, true, nil
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
func tokenizePath(p string) ([]pathToken, error) {
	var tokens []pathToken
	for _, seg := range strings.Split(p, ".") {
		if seg == "" {
			continue
		}
		var err error
		tokens, err = appendSegmentTokens(tokens, seg)
		if err != nil {
			return nil, err
		}
	}
	return tokens, nil
}

// appendSegmentTokens expands one dot-delimited segment, peeling any trailing
// "[N]" bracket subscripts (e.g. "files[0]" -> key "files" then index 0).
func appendSegmentTokens(tokens []pathToken, seg string) ([]pathToken, error) {
	name, brackets, err := splitBrackets(seg)
	if err != nil {
		return nil, err
	}
	if name != "" {
		tokens = append(tokens, classifySegment(name))
	}
	for _, b := range brackets {
		tokens = append(tokens, classifySegment(b))
	}
	return tokens, nil
}

// splitBrackets separates a segment's leading name from its trailing bracket
// subscripts: "files[0]" -> ("files", ["0"]); "[0]" -> ("", ["0"]); "name" ->
// ("name", nil). An unmatched '[' is malformed path syntax and returns an error
// rather than silently truncating to a wrong node.
func splitBrackets(seg string) (name string, subscripts []string, err error) {
	open := strings.IndexByte(seg, '[')
	if open < 0 {
		return seg, nil, nil
	}
	name = seg[:open]
	rest := seg[open:]
	for len(rest) > 0 && rest[0] == '[' {
		end := strings.IndexByte(rest, ']')
		if end < 0 {
			return "", nil, fmt.Errorf("malformed JSON path segment %q: unmatched '['", seg)
		}
		subscripts = append(subscripts, rest[1:end])
		rest = rest[end+1:]
	}
	return name, subscripts, nil
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
		return t
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

// joinArray renders a JSON array as Jackett does for a leaf array selection:
// String.Join(",", valueArray) over each element's canonical string.
func joinArray(arr []any) string {
	parts := make([]string, 0, len(arr))
	for _, e := range arr {
		parts = append(parts, canonicalString(e))
	}
	return strings.Join(parts, ",")
}
