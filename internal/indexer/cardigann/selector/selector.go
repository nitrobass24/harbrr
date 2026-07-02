package selector

import (
	"errors"
	"fmt"
	"strings"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/loader"
)

// ErrSelectorNoMatch reports that a required selector found nothing. Callers
// (the engine) distinguish required vs optional by inspecting the
// SelectorBlock; this stage simply reports found=false and, when the caller
// asked for a required value, wraps this sentinel. Error messages reference the
// selector/field only — never the matched content, which may embed a passkey.
var ErrSelectorNoMatch = errors.New("selector matched no element")

// EvalFunc is the injectable template-eval seam. Jackett interleaves Go-template
// evaluation into handleSelector (for selector strings, case values, text, and
// default). To keep this stage decoupled from the template package,
// the Engine takes an EvalFunc; New defaults it to identity so selector tests
// use literal values and the engine binds template.Eval later.
type EvalFunc func(string) (string, error)

// Engine extracts field values from parsed HTML/JSON documents, reproducing
// Jackett's CardigannIndexer.handleSelector / handleJsonSelector semantics.
type Engine struct {
	// EvalTemplate evaluates a Go-template fragment. Defaults to identity.
	EvalTemplate EvalFunc
}

// New constructs an Engine with EvalTemplate defaulting to identity.
func New() *Engine {
	return &Engine{EvalTemplate: identity}
}

func identity(s string) (string, error) { return s, nil }

// eval runs the injected template seam, defaulting to identity when unset so a
// zero-value Engine is still usable.
func (e *Engine) eval(s string) (string, error) {
	if e.EvalTemplate == nil {
		return s, nil
	}
	out, err := e.EvalTemplate(s)
	if err != nil {
		return "", fmt.Errorf("evaluating template fragment: %w", err)
	}
	return out, nil
}

// docKind distinguishes the two backends behind a Document/Row.
type docKind int

const (
	kindHTML docKind = iota
	kindJSON
)

// Document is a parsed response body (HTML or JSON) ready to be split into rows.
type Document struct {
	kind docKind
	html *htmlNode // root, kindHTML
	json *jsonNode // root, kindJSON
}

// Row is a single result row: one HTML element or one JSON element, scoping the
// per-field selector queries that follow.
type Row struct {
	kind docKind
	html *htmlNode
	json *jsonNode
}

// node is the narrow backend abstraction over which field extraction is written
// once. Both the goquery (cascadia) and JSON backends implement it. Kept to a
// handful of methods so the two implementations stay small and parallel.
type node interface {
	// query returns the first descendant (or self) matching sel, scoped to this
	// node. ok is false when nothing matched.
	query(sel string) (node, bool, error)
	// caseMatch reports whether this node satisfies a case-switch key. HTML uses
	// CSS matching (Matches(key) || QuerySelector(key) != null); JSON uses string
	// equality of the extracted value, with "*" as the wildcard.
	caseMatch(key string) (bool, error)
	// remove strips every descendant matching sel from this node's subtree.
	remove(sel string) error
	// attribute returns the named attribute. found is false when absent.
	attribute(name string) (value string, found bool)
	// text returns the node's normalized inner text (HTML) or canonical scalar
	// string (JSON leaf), matching Jackett's value extraction.
	text() string
}

// backend returns the node backing a Row.
func (r Row) backend() node {
	if r.kind == kindHTML {
		return r.html
	}
	return r.json
}

// Field reproduces handleSelector / handleJsonSelector for a single field.
//
// Order (HTML): text fallback → selector query (required-empty is an error,
// optional-empty returns found=false) → remove → case switch (first matching
// CSS key wins; "*" is the universal catch-all) → attribute vs normalized
// innerText. JSON mirrors this but the case switch compares the extracted value
// for string equality (with "*" catch-all) rather than re-querying.
//
// Template evaluation is interleaved exactly where Jackett applies it: on the
// selector string, on case values, and on text. The default is NOT applied here
// (Jackett applies it in the field loop); a non-optional empty result
// is reported via found=false so the caller decides between default and error.
func (e *Engine) Field(row Row, block loader.SelectorBlock) (value string, found bool, err error) {
	if block.Text != nil {
		v, err := e.eval(block.Text.String())
		if err != nil {
			return "", false, err
		}
		return v, true, nil
	}

	cur := row.backend()
	if block.Selector != "" {
		sel, err := e.eval(block.Selector)
		if err != nil {
			return "", false, err
		}
		next, ok, qerr := cur.query(sel)
		if qerr != nil {
			return "", false, fmt.Errorf("selector %q: %w", sel, qerr)
		}
		if !ok {
			return "", false, nil
		}
		cur = next
	}

	if block.Remove != "" {
		if rerr := cur.remove(block.Remove); rerr != nil {
			return "", false, fmt.Errorf("remove selector %q: %w", block.Remove, rerr)
		}
	}

	return e.extract(cur, block)
}

// extract performs the case/attribute/text branch of handleSelector after the
// selector and remove phases have positioned cur.
//
// Known minor divergence (not corpus-reachable): for a JSON field with NO
// selector/case/attribute/text, Jackett leaves value=null and returns "" (the
// JSON handler has no TextContent default), whereas the default branch here reads
// the row node's canonical string. No vendored JSON def authors a selector-less
// field, so this path is never exercised; the engine's required/optional handling
// would mask it regardless. Documented rather than special-cased.
func (e *Engine) extract(cur node, block loader.SelectorBlock) (string, bool, error) {
	switch {
	case len(block.Case) > 0:
		return e.applyCase(cur, block.Case)
	case block.Attribute != "":
		v, ok := cur.attribute(block.Attribute)
		if !ok {
			return "", false, nil
		}
		return normalizeSpace(v), true, nil
	default:
		return normalizeSpace(cur.text()), true, nil
	}
}

// applyCase evaluates the case switch. Each key is tested against cur in
// definition order; the first match yields its template-evaluated value. "*" is
// the catch-all (universal CSS selector for HTML; explicit wildcard for JSON).
// No match returns found=false (the caller treats this as required-error or
// optional-skip).
func (e *Engine) applyCase(cur node, cases map[string]loader.Scalar) (string, bool, error) {
	for _, c := range orderedCases(cases) {
		ok, err := cur.caseMatch(c.key)
		if err != nil {
			return "", false, fmt.Errorf("case selector %q: %w", c.key, err)
		}
		if !ok {
			continue
		}
		v, err := e.eval(c.value.String())
		if err != nil {
			return "", false, err
		}
		return v, true, nil
	}
	return "", false, nil
}

// trimDotPrefix mirrors handleJsonSelector's Selector.TrimStart('.') so a
// leading-dot JSON selector resolves identically to its undotted form.
func trimDotPrefix(s string) string { return strings.TrimLeft(s, ".") }
