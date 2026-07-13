package selector

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/loader"
)

// jsonNode adapts a parsed JSON value (the result of encoding/json into any) to
// the node interface, with a path resolver mirroring Newtonsoft SelectToken for
// the dotted/indexed subset the corpus uses.
type jsonNode struct {
	// value is the JSON value this node points at (map, slice, scalar, or nil).
	value any
	// root is the full row element for a row node reshaped by rows.attribute: a
	// field selector beginning ".." resolves against it (Jackett's parentObj =
	// Row escape) instead of value. nil means root == value (the common case).
	root any
	// cached canonical string of value, computed lazily by text().
	str    string
	strSet bool
}

// rowRoot returns the full row element for a ".." escape, defaulting to value.
func (n *jsonNode) rowRoot() any {
	if n.root != nil {
		return n.root
	}
	return n.value
}

// ParseJSON parses a JSON response body into a Document.
func (e *Engine) ParseJSON(body []byte) (*Document, error) {
	var v any
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	if err := dec.Decode(&v); err != nil {
		return nil, fmt.Errorf("parsing JSON document: %w", err)
	}
	return &Document{kind: kindJSON, json: &jsonNode{value: v}}, nil
}

func (n *jsonNode) query(sel string) (node, bool, error) {
	// A field selector beginning ".." escapes to the full row element (Jackett:
	// parentObj = Row), so a field can read outside the rows.attribute sub-object.
	// The leading dots are then stripped like any other JSON selector.
	base := n.value
	if strings.HasPrefix(sel, "..") {
		base = n.rowRoot()
	}

	// Jackett handleJsonSelector first resolves the pseudo-selector conditions
	// (:has/:not/:contains) via JsonParseFieldSelector to get the field PATH, then
	// SelectToken(path) reads the value. A plain dotted path has no filters, so
	// jsonFieldSelector reduces to a path-existence check (the previous behavior).
	path, ok := jsonFieldSelector(base, trimDotPrefix(sel))
	if !ok {
		return nil, false, nil
	}
	v, ok := resolvePath(base, path)
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
// in the engine).
func (d *Document) jsonRows(block loader.RowsBlock) ([]Row, error) {
	// Jackett evaluates rows.count first: a count selector that parses to < 1
	// short-circuits the path to zero rows (a parse failure is ignored).
	if d.jsonCountIsZero(block.Count) {
		return nil, nil
	}

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

	return d.buildJSONRows(arr, block.Attribute), nil
}

// jsonCountIsZero reports whether rows.count resolves to an integer < 1. Jackett
// uses int.TryParse + "count < 1", so a missing/non-integer count never
// short-circuits (the parse simply fails and is ignored).
func (d *Document) jsonCountIsZero(count *loader.SelectorBlock) bool {
	if count == nil || count.Selector == "" {
		return false
	}
	node, ok, err := d.json.query(count.Selector)
	if err != nil || !ok {
		return false
	}
	n, err := strconv.Atoi(strings.TrimSpace(node.text()))
	return err == nil && n < 1
}

// buildJSONRows turns the resolved array into rows. When rows.attribute is set,
// each row is reshaped to that sub-object for field extraction (Jackett: selObj =
// Row.SelectToken(Attribute)); the full element is kept as the node root so a
// ".." field can still escape to it. A row whose attribute sub-object is absent
// is skipped — Jackett skips it under MissingAttributeEqualsNoResults and would
// otherwise dereference null; harbrr degrades cleanly in both cases.
func (d *Document) buildJSONRows(arr []any, attribute string) []Row {
	rows := make([]Row, 0, len(arr))
	for _, e := range arr {
		value := e
		if attribute != "" {
			sub, ok := resolvePath(e, attribute)
			if !ok {
				continue
			}
			value = sub
		}
		rows = append(rows, Row{kind: kindJSON, json: &jsonNode{value: value, root: e}})
	}
	return rows
}

// boolVal dereferences an optional bool flag, defaulting to false.
func boolVal(p *bool) bool { return p != nil && *p }
