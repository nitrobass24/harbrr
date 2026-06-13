package selector

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"

	"github.com/PuerkitoBio/goquery"
	"golang.org/x/net/html"
)

// ParseXML parses an XML response into a Document by building an html.Node tree
// from the XML token stream, then querying it with the same cascadia engine the
// HTML backend uses. Cardigann has a dedicated XML response mode (Jackett's
// AngleSharp XmlParser, CardigannIndexer.cs: `Response.Type == "xml"`), distinct
// from HTML: <link> and <title> are ordinary elements (not void/raw-text as the
// HTML5 parser treats them), so an RSS/Newznab feed's row selectors resolve
// correctly. Building the tree ourselves — rather than feeding XML to the HTML5
// parser — reproduces that.
//
// Qualified names are preserved (e.g. <torznab:attr> stays "torznab:attr") so a
// def's `torznab\:attr` selector matches, by mapping each element's resolved
// namespace back to its declared prefix.
func (e *Engine) ParseXML(body []byte) (*Document, error) {
	root, err := xmlToNode(body)
	if err != nil {
		return nil, fmt.Errorf("parsing XML document: %w", err)
	}
	doc := goquery.NewDocumentFromNode(root)
	return &Document{kind: kindHTML, html: &htmlNode{sel: doc.Selection}}, nil
}

// xmlToNode decodes XML into an html.Node document tree. It tracks xmlns
// declarations so a namespaced element/attribute keeps its prefix:local name,
// matching how Jackett's selectors reference RSS/Newznab namespaces.
func xmlToNode(body []byte) (*html.Node, error) {
	root := &html.Node{Type: html.DocumentNode}
	dec := xml.NewDecoder(bytes.NewReader(body))
	dec.Strict = false

	prefixes := map[string]string{} // namespace URI -> declared prefix ("" = default)
	cur := root
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("decoding XML token: %w", err)
		}
		cur = consumeToken(tok, cur, prefixes)
	}
	return root, nil
}

// consumeToken folds one XML token into the tree and returns the new current
// node (descending on a start element, ascending on an end element).
func consumeToken(tok xml.Token, cur *html.Node, prefixes map[string]string) *html.Node {
	switch t := tok.(type) {
	case xml.StartElement:
		recordNamespaces(t.Attr, prefixes)
		n := &html.Node{Type: html.ElementNode, Data: qualifyName(t.Name, prefixes), Attr: elementAttrs(t.Attr, prefixes)}
		cur.AppendChild(n)
		return n
	case xml.EndElement:
		if cur.Parent != nil {
			return cur.Parent
		}
		return cur
	case xml.CharData:
		cur.AppendChild(&html.Node{Type: html.TextNode, Data: string(t)})
		return cur
	default:
		return cur
	}
}

// recordNamespaces registers xmlns / xmlns:prefix declarations so later elements
// in scope resolve their namespace URI back to the declared prefix.
func recordNamespaces(attrs []xml.Attr, prefixes map[string]string) {
	for _, a := range attrs {
		switch {
		case a.Name.Space == "xmlns":
			prefixes[a.Value] = a.Name.Local
		case a.Name.Space == "" && a.Name.Local == "xmlns":
			prefixes[a.Value] = ""
		}
	}
}

// elementAttrs converts XML attributes to html.Attribute, dropping xmlns
// declarations (structural, not selectable) and preserving qualified names.
func elementAttrs(attrs []xml.Attr, prefixes map[string]string) []html.Attribute {
	out := make([]html.Attribute, 0, len(attrs))
	for _, a := range attrs {
		if a.Name.Space == "xmlns" || (a.Name.Space == "" && a.Name.Local == "xmlns") {
			continue
		}
		out = append(out, html.Attribute{Key: qualifyName(a.Name, prefixes), Val: a.Value})
	}
	return out
}

// qualifyName renders an xml.Name as the prefix:local qualified name a selector
// references. A declared namespace resolves the URI in Name.Space back to its
// prefix; an undeclared one (Strict=false) leaves the literal prefix in
// Name.Space; the default namespace and unprefixed names yield the bare local.
func qualifyName(name xml.Name, prefixes map[string]string) string {
	if name.Space == "" {
		return name.Local
	}
	if prefix, ok := prefixes[name.Space]; ok {
		if prefix == "" {
			return name.Local
		}
		return prefix + ":" + name.Local
	}
	return name.Space + ":" + name.Local
}
