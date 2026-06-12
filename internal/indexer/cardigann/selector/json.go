package selector

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/autobrr/seekbrr/internal/indexer/cardigann/loader"
)

// jsonNode adapts a parsed JSON value (the result of encoding/json into any) to
// the node interface, with a path resolver mirroring Newtonsoft SelectToken for
// the dotted/indexed subset the corpus uses.
type jsonNode struct {
	// value is the JSON value this node points at (map, slice, scalar, or nil).
	value any
	// cached canonical string of value, computed lazily by text().
	str    string
	strSet bool
}

// ParseJSON parses a JSON response body into a Document.
func (e *Engine) ParseJSON(body []byte) (*Document, error) {
	var v any
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	if err := dec.Decode(&v); err != nil {
		return nil, fmt.Errorf("parsing JSON document: %w", err)
	}
	// Reject trailing data after the first value, matching Json.NET (Jackett),
	// which errors on "additional text encountered after finished reading JSON".
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		if err != nil {
			return nil, fmt.Errorf("parsing JSON document: trailing data: %w", err)
		}
		return nil, errors.New("parsing JSON document: unexpected trailing data after top-level value")
	}
	return &Document{kind: kindJSON, json: &jsonNode{value: v}}, nil
}

func (n *jsonNode) query(sel string) (node, bool, error) {
	v, ok, err := resolvePath(n.value, trimDotPrefix(sel))
	if err != nil {
		return nil, false, err
	}
	if !ok {
		return nil, false, nil
	}
	return &jsonNode{value: v}, true, nil
}

// caseMatch reproduces handleJsonSelector's case test: the extracted value
// equals the key, or the key is the "*" wildcard.
func (n *jsonNode) caseMatch(key string) (bool, error) {
	if key == "*" {
		return true, nil
	}
	return n.text() == key, nil
}

// remove is a no-op for JSON: Jackett's Selector.Remove applies only to the DOM
// backend. handleJsonSelector has no remove phase.
func (n *jsonNode) remove(string) error { return nil }

// attribute is unused for JSON (definitions select via path, not attribute);
// handleJsonSelector has no attribute branch. Always reports absent.
func (n *jsonNode) attribute(string) (string, bool) { return "", false }

// text returns the canonical string form of the JSON leaf, matching
// JToken.ToString()/JArray join semantics: scalars render canonically, an array
// joins its elements with commas, and a null/object renders empty.
func (n *jsonNode) text() string {
	if n.strSet {
		return n.str
	}
	n.str = canonicalString(n.value)
	n.strSet = true
	return n.str
}

// jsonRows splits a JSON Document into result rows. rows.selector is "$" (root,
// expected to be an array) or a path to an array; each element becomes a Row.
// Jackett's JSON row handler has no "after" merge (that is HTML-only), so After
// is ignored here, matching Jackett. The only JSON-specific row behavior at this
// layer is MissingAttributeEqualsNoResults, which turns a missing/non-array
// selector into "0 rows" instead of an error (field-level Multiple handling lives
// in the engine, item 10).
func (d *Document) jsonRows(block loader.RowsBlock) ([]Row, error) {
	arr, ok, err := resolveRowsArray(d.json.value, block.Selector)
	if err != nil {
		return nil, err
	}
	if !ok {
		// Jackett: a missing rows array is "0 rows" only when
		// MissingAttributeEqualsNoResults is set; otherwise it is an error.
		if boolVal(block.MissingAttributeEqualsNoResults) {
			return nil, nil
		}
		return nil, fmt.Errorf("rows selector %q: %w", block.Selector, ErrSelectorNoMatch)
	}

	rows := make([]Row, 0, len(arr))
	for i := range arr {
		rows = append(rows, Row{kind: kindJSON, json: &jsonNode{value: arr[i]}})
	}
	return rows, nil
}

// boolVal dereferences an optional bool flag, defaulting to false.
func boolVal(p *bool) bool { return p != nil && *p }
