package selector

import (
	"bytes"
	"encoding/json"
	"strings"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/loader"
)

// Order-preserving decode for the ROWS NODE ONLY (#681).
//
// encoding/json discards JSON object property order, but Jackett expands an
// object-shaped rows.multiple with Newtonsoft's selObj.Values<JObject>(), which
// yields the properties in DOCUMENT order — and that order is the release order,
// so it decides what lands on page 1 under a cap. Rather than reshape the generic
// decode the whole selector path is written against, we re-read the raw body for
// this one node and recover the key order of the object each row expands.
//
// The array shape never comes through here: a JSON array already decodes in
// order, so buildJSONRows keeps using the parsed slice byte-for-byte.

// rowKeyOrders returns, per element of the rows array, the property names of the
// object rows.multiple expands, in document order. Entry i is nil when that
// element (after rows.attribute) is not an object.
//
// It returns nil — "no order available, fall back" — when the rows selector
// carries a pseudo-selector filter: resolveRowsArray DROPS elements the filter
// rejects, so the parsed slice no longer lines up index-for-index with the raw
// one. No vendored definition combines multiple with a filtered rows selector.
func (d *Document) rowKeyOrders(block loader.RowsBlock) [][]string {
	if len(d.raw) == 0 || rowsFilters(block.Selector) != "" {
		return nil
	}
	node, ok := rawResolve(d.raw, rowsPath(block.Selector))
	if !ok {
		return nil
	}
	var elems []json.RawMessage
	if json.Unmarshal(node, &elems) != nil {
		return nil
	}

	orders := make([][]string, len(elems))
	for i, e := range elems {
		if block.Attribute != "" {
			sub, ok := rawResolve(e, block.Attribute)
			if !ok {
				continue
			}
			e = sub
		}
		orders[i] = rawObjectKeys(e)
	}
	return orders
}

// rawResolve walks the same Newtonsoft-style path subset as resolvePath, but over
// raw JSON, so the bytes of the target node survive with their property order.
func rawResolve(raw json.RawMessage, path string) (json.RawMessage, bool) {
	cur := raw
	for _, tok := range tokenizePath(trimDotPrefix(strings.TrimPrefix(strings.TrimSpace(path), "$"))) {
		next, ok := rawDescend(cur, tok)
		if !ok {
			return nil, false
		}
		cur = next
	}
	return cur, true
}

func rawDescend(cur json.RawMessage, tok pathToken) (json.RawMessage, bool) {
	if tok.isIdx {
		var arr []json.RawMessage
		if json.Unmarshal(cur, &arr) != nil || tok.index < 0 || tok.index >= len(arr) {
			return nil, false
		}
		return arr[tok.index], true
	}
	var obj map[string]json.RawMessage
	if json.Unmarshal(cur, &obj) != nil {
		return nil, false
	}
	v, ok := obj[tok.key]
	return v, ok
}

// rawObjectKeys returns the property names of a raw JSON object in document
// order, or nil when raw is not an object. It reads the tokens directly (the only
// place the order is still intact) and skips each value wholesale.
func rawObjectKeys(raw json.RawMessage) []string {
	dec := json.NewDecoder(bytes.NewReader(raw))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return nil
	}
	var keys []string
	for dec.More() {
		key, err := dec.Token()
		if err != nil {
			return nil
		}
		name, ok := key.(string)
		if !ok {
			return nil
		}
		keys = append(keys, name)
		var skip json.RawMessage
		if dec.Decode(&skip) != nil {
			return nil
		}
	}
	return keys
}
