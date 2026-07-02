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
// declarations per element scope so a namespaced element/attribute keeps its
// prefix:local name (matching how Jackett's selectors reference RSS/Newznab
// namespaces) and so a nested redeclaration never leaks into a sibling.
func xmlToNode(body []byte) (*html.Node, error) {
	root := &html.Node{Type: html.DocumentNode}
	dec := xml.NewDecoder(bytes.NewReader(body))
	dec.Strict = false

	// scopes is a stack of per-element xmlns declarations (namespace URI ->
	// prefix, "" for the default namespace), pushed on a start tag and popped on
	// the matching end tag. qualifyName resolves a URI innermost-out, so an
	// element only sees declarations in its own ancestry.
	var scopes []map[string]string
	cur := root
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("decoding XML token: %w", err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			scopes = append(scopes, namespaceDecls(t.Attr))
			n := &html.Node{Type: html.ElementNode, Data: qualifyName(t.Name, scopes), Attr: elementAttrs(t.Attr, scopes)}
			cur.AppendChild(n)
			cur = n
		case xml.EndElement:
			if len(scopes) > 0 {
				scopes = scopes[:len(scopes)-1]
			}
			if cur.Parent != nil {
				cur = cur.Parent
			}
		case xml.CharData:
			cur.AppendChild(&html.Node{Type: html.TextNode, Data: string(t)})
		}
	}
	return root, nil
}

// namespaceDecls extracts an element's own xmlns / xmlns:prefix declarations as a
// namespace-URI -> prefix map ("" for the default namespace); nil when the
// element declares none.
func namespaceDecls(attrs []xml.Attr) map[string]string {
	var decls map[string]string
	for _, a := range attrs {
		switch {
		case a.Name.Space == "xmlns":
			if decls == nil {
				decls = map[string]string{}
			}
			decls[a.Value] = a.Name.Local
		case a.Name.Space == "" && a.Name.Local == "xmlns":
			if decls == nil {
				decls = map[string]string{}
			}
			decls[a.Value] = ""
		}
	}
	return decls
}

// elementAttrs converts XML attributes to html.Attribute, dropping xmlns
// declarations (structural, not selectable) and preserving qualified names.
func elementAttrs(attrs []xml.Attr, scopes []map[string]string) []html.Attribute {
	out := make([]html.Attribute, 0, len(attrs))
	for _, a := range attrs {
		if a.Name.Space == "xmlns" || (a.Name.Space == "" && a.Name.Local == "xmlns") {
			continue
		}
		out = append(out, html.Attribute{Key: qualifyName(a.Name, scopes), Val: a.Value})
	}
	return out
}

// qualifyName renders an xml.Name as the prefix:local qualified name a selector
// references, resolving the URI in Name.Space to its prefix via the innermost
// enclosing xmlns declaration. The default namespace and unprefixed names yield
// the bare local; an undeclared namespace (Strict=false) leaves the literal
// prefix in Name.Space.
func qualifyName(name xml.Name, scopes []map[string]string) string {
	if name.Space == "" {
		return name.Local
	}
	for i := len(scopes) - 1; i >= 0; i-- {
		if prefix, ok := scopes[i][name.Space]; ok {
			if prefix == "" {
				return name.Local
			}
			return prefix + ":" + name.Local
		}
	}
	return name.Space + ":" + name.Local
}
