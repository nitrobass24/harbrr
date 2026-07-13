package search

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// filterJSONJoinArray implements jsonjoinarray[jsonpath,separator]: parse the
// value as JSON, select the array at the (dotted) JSONPath, and join its
// elements' string forms with the separator. Jackett uses Json.NET's
// SelectToken; definitions use simple dotted paths, which we support here.
//
// No corpus definition currently exercises this tail filter, so the
// implementation covers the dotted-path / array shapes Jackett's templates use
// without porting a full JSONPath engine.
func filterJSONJoinArray(value string, args []string) (string, error) {
	if len(args) < 2 {
		return "", fmt.Errorf("jsonjoinarray needs 2 args, got %d: %w", len(args), errMissingArg)
	}
	path, sep := args[0], args[1]

	var root any
	if err := json.Unmarshal([]byte(value), &root); err != nil {
		return "", fmt.Errorf("jsonjoinarray: parsing JSON: %w", err)
	}

	token, err := selectToken(root, path)
	if err != nil {
		return "", fmt.Errorf("jsonjoinarray: %w", err)
	}

	arr, ok := token.([]any)
	if !ok {
		return "", errors.New("jsonjoinarray: selected token is not an array")
	}

	parts := make([]string, 0, len(arr))
	for _, el := range arr {
		parts = append(parts, scalarString(el))
	}
	return strings.Join(parts, sep), nil
}

// selectToken walks a dotted JSONPath (leading "$"/"$." optional) over a parsed
// JSON value, descending through object keys. It is intentionally minimal — the
// dotted-key subset Jackett definitions use — not a full JSONPath engine.
func selectToken(root any, path string) (any, error) {
	p := strings.TrimPrefix(path, "$")
	p = strings.TrimPrefix(p, ".")
	cur := root
	if p == "" {
		return cur, nil
	}
	for _, key := range strings.Split(p, ".") {
		obj, ok := cur.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("path segment %q: not an object", key)
		}
		next, ok := obj[key]
		if !ok {
			return nil, fmt.Errorf("path segment %q: not found", key)
		}
		cur = next
	}
	return cur, nil
}

// scalarString renders a JSON scalar the way Json.NET's ToString() would for
// the common cases: strings verbatim, numbers/bools via JSON marshaling.
func scalarString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(b)
}

// filterValidate implements validate[allowlist]: tokenize both the value and
// the (lowercased) allowlist on Jackett's delimiter set, intersect them, and
// return the matches comma-joined (preserving allowlist order). A non-match
// yields "". This passes through only the recognized tokens.
func filterValidate(value string, args []string) (string, error) {
	allow := tokenizeValidate(strings.ToLower(firstArg(args)))
	present := make(map[string]struct{}, len(value))
	for _, tok := range tokenizeValidate(strings.ToLower(value)) {
		present[tok] = struct{}{}
	}

	seen := make(map[string]struct{}, len(allow))
	matched := make([]string, 0, len(allow))
	for _, tok := range allow {
		if _, dup := seen[tok]; dup {
			continue
		}
		seen[tok] = struct{}{}
		if _, ok := present[tok]; ok {
			matched = append(matched, tok)
		}
	}
	return strings.Join(matched, ","), nil
}

// tokenizeValidate splits on Jackett's validate delimiter set
// {',', ' ', '/', ')', '(', '.', ';', '[', ']', '"', '|', ':'}, dropping empty
// tokens, mirroring String.Split(delimiters, RemoveEmptyEntries).
func tokenizeValidate(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool {
		switch r {
		case ',', ' ', '/', ')', '(', '.', ';', '[', ']', '"', '|', ':':
			return true
		default:
			return false
		}
	})
}
