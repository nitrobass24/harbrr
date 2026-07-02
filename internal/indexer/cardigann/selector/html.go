package selector

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/PuerkitoBio/goquery"
	"github.com/andybalholm/cascadia"
	"golang.org/x/net/html"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/loader"
)

// htmlNode adapts a goquery.Selection (one element) to the node interface. All
// CSS matching goes through cascadia, the same engine goquery uses, so the
// standing fixture suite checks cascadia-vs-AngleSharp compatibility directly.
type htmlNode struct {
	sel *goquery.Selection
}

// ParseHTML parses an HTML response body into a Document.
func (e *Engine) ParseHTML(body []byte) (*Document, error) {
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("parsing HTML document: %w", err)
	}
	return &Document{kind: kindHTML, html: &htmlNode{sel: doc.Selection}}, nil
}

// Root returns the whole document as a single Row, scoping a selector query to
// the entire document rather than to a per-result row. The login/session stage
// uses it to reproduce Jackett's checkForError / TestLogin, which evaluate their
// selectors against ResultDocument.QuerySelector (the whole page), not a row.
// HTML only; JSON documents have no document-root analogue here.
func (d *Document) Root() Row {
	return Row{kind: d.kind, html: d.html, json: d.json}
}

// query reproduces Jackett's field-selector resolution:
//
//	selection = Dom.Matches(sel) ? Dom : QuerySelector(Dom, sel)
//
// i.e. the current element may satisfy the selector itself (self-match) before we
// search its descendants. matchSelector also reproduces Jackett's QuerySelector
// :root handling, since cascadia (unlike AngleSharp) implements neither
// self-match nor :root.
func (n *htmlNode) query(sel string) (node, bool, error) {
	found, ok, err := n.matchSelector(sel)
	if err != nil || !ok {
		return nil, false, err
	}
	return &htmlNode{sel: found}, true, nil
}

// caseMatch reproduces Jackett's HTML case test: selection.Matches(key) ||
// QuerySelector(selection, key) != null. "*" is the universal selector, so it
// matches every element here without special-casing. :root re-rooting applies
// identically (Jackett routes both through the same Matches/QuerySelector pair).
func (n *htmlNode) caseMatch(key string) (bool, error) {
	_, ok, err := n.matchSelector(key)
	return ok, err
}

// matchSelector implements Jackett's "Matches(sel) ? self : QuerySelector(sel)".
// It returns the matched selection (the element itself on self-match, otherwise
// the first matching descendant). A leading ":root" is stripped and the search is
// re-rooted at the document element, exactly as Jackett's QuerySelector helper
// does (AngleSharp/cascadia handle :root specially, so we do it manually).
func (n *htmlNode) matchSelector(sel string) (*goquery.Selection, bool, error) {
	root, rest := splitRoot(sel)
	scope := n.sel
	if root {
		scope = documentRoot(n.sel)
		// Bare ":root" resolves to the root element itself.
		if rest == "" {
			return scope.First(), scope.Length() > 0, nil
		}
	}

	matcher, err := compileCSS(rest)
	if err != nil {
		return nil, false, err
	}

	// Self-match: Matches(sel) ? Dom. For :root this tests the re-rooted element.
	if scope.IsMatcher(matcher) {
		return scope.First(), true, nil
	}
	// Otherwise QuerySelector: first matching descendant (descendant-only,
	// matching AngleSharp's Element.QuerySelector).
	found := scope.FindMatcher(matcher)
	if found.Length() == 0 {
		return nil, false, nil
	}
	return found.First(), true, nil
}

// splitRoot detects a leading ":root" prefix (Jackett's manual handling, since
// AngleSharp/cascadia treat it specially) and returns the remaining selector with
// the prefix stripped. Matches Jackett QuerySelector's Selector.Substring(5).
func splitRoot(sel string) (isRoot bool, rest string) {
	s := strings.TrimSpace(sel)
	if !strings.HasPrefix(s, ":root") {
		return false, s
	}
	return true, strings.TrimSpace(s[len(":root"):])
}

// documentRoot walks up to the topmost element, reproducing Jackett's
// QuerySelector :root re-rooting (while ParentElement != null).
func documentRoot(sel *goquery.Selection) *goquery.Selection {
	n := sel.Get(0)
	if n == nil {
		return sel
	}
	for n.Parent != nil && n.Parent.Type == html.ElementNode {
		n = n.Parent
	}
	return goquery.NewDocumentFromNode(n).Selection
}

func (n *htmlNode) remove(sel string) error {
	matcher, err := compileCSS(sel)
	if err != nil {
		return err
	}
	n.sel.FindMatcher(matcher).Remove()
	return nil
}

func (n *htmlNode) attribute(name string) (string, bool) {
	return n.sel.Attr(name)
}

// text returns the element's raw inner text: the concatenation of all descendant
// text nodes, equivalent to AngleSharp's IElement.TextContent (which, like
// goquery's Text(), preserves source whitespace verbatim — it does not collapse
// runs). Jackett applies ParseUtil.NormalizeSpace (a trim) afterward; that trim
// lives in extract -> normalizeSpace, not here.
func (n *htmlNode) text() string {
	return n.sel.Text()
}

// Rows splits a Document into result rows per the rows block. For HTML, each
// element matching rows.selector becomes one Row (cascadia query over the whole
// document). After merges the next N siblings' children into each row (Jackett's
// "after" pagination flattening); Multiple / MissingAttributeEqualsNoResults are
// JSON-only and ignored for HTML, matching Jackett.
func (d *Document) Rows(block loader.RowsBlock) ([]Row, error) {
	if d.kind == kindJSON {
		return d.jsonRows(block)
	}
	return d.htmlRows(block)
}

func (d *Document) htmlRows(block loader.RowsBlock) ([]Row, error) {
	if block.Selector == "" {
		return nil, fmt.Errorf("rows: %w: html rows require a selector", ErrSelectorNoMatch)
	}
	matcher, err := compileCSS(block.Selector)
	if err != nil {
		return nil, err
	}
	matched := d.html.sel.FindMatcher(matcher)

	elems := make([]*goquery.Selection, 0, matched.Length())
	matched.Each(func(_ int, s *goquery.Selection) {
		elems = append(elems, s)
	})

	elems = mergeAfter(elems, block.After)

	rows := make([]Row, 0, len(elems))
	for _, s := range elems {
		rows = append(rows, Row{kind: kindHTML, html: &htmlNode{sel: s}})
	}
	return rows, nil
}

// mergeAfter reproduces Jackett's "after" handling: for each kept row, the child
// nodes of the following `after` rows are appended into it, and those rows are
// dropped. The result is one merged row per group of (1 + after) source rows.
func mergeAfter(elems []*goquery.Selection, after *int) []*goquery.Selection {
	n := 0
	if after != nil {
		n = *after
	}
	if n <= 0 || len(elems) == 0 {
		return elems
	}

	out := make([]*goquery.Selection, 0, len(elems))
	for i := 0; i < len(elems); i += n + 1 {
		current := elems[i]
		for j := 1; j <= n && i+j < len(elems); j++ {
			appendChildren(current, elems[i+j])
		}
		out = append(out, current)
	}
	return out
}

// appendChildren moves the child nodes of src under dst, mirroring
// CurrentRow.Append(MergeRow.ChildNodes).
func appendChildren(dst, src *goquery.Selection) {
	dstNode := dst.Get(0)
	srcNode := src.Get(0)
	if dstNode == nil || srcNode == nil {
		return
	}
	for c := srcNode.FirstChild; c != nil; {
		next := c.NextSibling
		srcNode.RemoveChild(c)
		dstNode.AppendChild(c)
		c = next
	}
}

// compileCSS compiles a CSS selector with cascadia, the engine goquery uses.
// cascadia.Compile yields a cascadia.Selector, which satisfies goquery.Matcher.
// A compile failure is surfaced (never silenced): it means cascadia rejects a
// construct AngleSharp accepts, which the corpus census tracks as a known
// incompatibility. The error references only the selector text.
func compileCSS(sel string) (cascadia.Selector, error) {
	s := strings.TrimSpace(sel)
	c, err := cascadia.Compile(s)
	if err != nil {
		return nil, fmt.Errorf("compiling css selector %q: %w", s, err)
	}
	return c, nil
}
