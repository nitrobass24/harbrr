package search

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/internal/selector"
)

// filterJSONJoinArray implements jsonjoinarray[jsonpath,separator]: parse the
// value as JSON, select the array at the (dotted) JSONPath, and join its
// elements' string forms with the separator. Jackett uses Json.NET's
// SelectToken; the path walk is selector.ResolvePath, the same Newtonsoft-style
// subset the JSON selector backend resolves with.
//
// No corpus definition currently exercises this tail filter.
func filterJSONJoinArray(value string, args []string) (string, error) {
	if len(args) < 2 {
		return "", fmt.Errorf("jsonjoinarray needs 2 args, got %d: %w", len(args), errMissingArg)
	}
	path, sep := args[0], args[1]

	var root any
	if err := json.Unmarshal([]byte(value), &root); err != nil {
		return "", fmt.Errorf("jsonjoinarray: parsing JSON: %w", err)
	}

	token, ok := selector.ResolvePath(root, path)
	if !ok {
		return "", fmt.Errorf("jsonjoinarray: path %q not found", path)
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
