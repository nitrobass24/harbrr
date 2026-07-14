package selector

import (
	"testing"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/loader"
)

// TestParseXML proves the XML backend parses an RSS/Newznab feed the way
// Jackett's XmlParser does, where the HTML5 parser would not: <link> and <title>
// round-trip as ordinary text-bearing elements (in HTML, <link> is void and
// <title> is raw-text), and a namespaced <torznab:attr> keeps its prefix so a
// `torznab\:attr` selector matches.
func TestParseXML(t *testing.T) {
	t.Parallel()

	const feed = `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0" xmlns:torznab="http://torznab.com/schemas/2015/feed">
  <channel>
    <title>Feed Title</title>
    <item>
      <title>First Release</title>
      <link>https://xml.test/dl/1.torrent</link>
      <torznab:attr name="seeders" value="42" />
    </item>
    <item>
      <title>Second Release</title>
      <link>https://xml.test/dl/2.torrent</link>
      <torznab:attr name="seeders" value="7" />
    </item>
  </channel>
</rss>`

	doc, err := New().ParseXML([]byte(feed))
	if err != nil {
		t.Fatalf("ParseXML: %v", err)
	}

	rows, err := doc.Rows(loader.RowsBlock{Selector: "rss > channel > item"})
	if err != nil {
		t.Fatalf("Rows: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(rows))
	}

	title, found, err := New().Field(rows[0], loader.SelectorBlock{Selector: "title"}, nil)
	if err != nil || !found {
		t.Fatalf("title: found=%v err=%v", found, err)
	}
	if title != "First Release" {
		t.Errorf("title = %q, want First Release", title)
	}

	// <link> round-trips as text (the HTML5 parser would treat it as void and
	// the URL would leak out as a sibling).
	link, found, err := New().Field(rows[0], loader.SelectorBlock{Selector: "link"}, nil)
	if err != nil || !found {
		t.Fatalf("link: found=%v err=%v", found, err)
	}
	if link != "https://xml.test/dl/1.torrent" {
		t.Errorf("link = %q, want the torrent URL (must round-trip in XML)", link)
	}

	// The namespaced attr is selectable by its qualified name.
	seeders, found, err := New().Field(rows[0], loader.SelectorBlock{
		Selector:  `torznab\:attr[name="seeders"]`,
		Attribute: "value",
	}, nil)
	if err != nil || !found {
		t.Fatalf("torznab:attr seeders: found=%v err=%v", found, err)
	}
	if seeders != "42" {
		t.Errorf("seeders = %q, want 42", seeders)
	}
}

// TestParseXMLNamespaceScoping proves a nested xmlns redeclaration does not leak
// into a sibling: <child> rebinds the urn:ns namespace to prefix "b", but the
// <a:sibling> that follows it (outside child) must keep the root's prefix "a".
// With a flat, non-scoped prefix map the sibling would be mislabeled "b:sibling".
func TestParseXMLNamespaceScoping(t *testing.T) {
	t.Parallel()

	const feed = `<root xmlns:a="urn:ns">
  <child xmlns:b="urn:ns"><b:inner/></child>
  <a:sibling/>
</root>`

	doc, err := New().ParseXML([]byte(feed))
	if err != nil {
		t.Fatalf("ParseXML: %v", err)
	}

	// The sibling keeps the root prefix "a".
	if _, found, err := New().Field(doc.Root(), loader.SelectorBlock{Selector: `a\:sibling`}, nil); err != nil || !found {
		t.Fatalf("a:sibling not found (found=%v err=%v) — root prefix lost", found, err)
	}
	// It must NOT have leaked the inner prefix "b".
	if _, leaked, err := New().Field(doc.Root(), loader.SelectorBlock{Selector: `b\:sibling`}, nil); err != nil {
		t.Fatalf("b:sibling query error: %v", err)
	} else if leaked {
		t.Error("sibling mislabeled b:sibling — nested namespace prefix leaked")
	}
	// The inner element inside child correctly uses prefix "b".
	if _, found, err := New().Field(doc.Root(), loader.SelectorBlock{Selector: `b\:inner`}, nil); err != nil || !found {
		t.Errorf("b:inner not found (found=%v err=%v)", found, err)
	}
}

// TestParseXMLMixedCaseNames proves mixed-case element and attribute names are
// selectable. cascadia ASCII-lowercases type selectors and attribute keys at
// compile time and then compares exactly, so the tree must carry lowercased
// names (as html.Parse produces for the HTML backend) or a def's
// `selector: pubDate` — RSS's canonical casing, used by every vendored XML
// def's date field — never matches and the required date miss drops every row.
// Attribute VALUES and text content must keep their original case.
func TestParseXMLMixedCaseNames(t *testing.T) {
	t.Parallel()

	const feed = `<rss xmlns:TZ="urn:test"><channel><item>
  <title>Mixed Case</title>
  <pubDate>Mon, 02 Jan 2006 15:04:05 -0700</pubDate>
  <enclosure URL="https://xml.test/dl/MixedCase.torrent" contentLength="123"/>
  <TZ:Info name="Seeders" value="42"/>
</item></channel></rss>`

	doc, err := New().ParseXML([]byte(feed))
	if err != nil {
		t.Fatalf("ParseXML: %v", err)
	}
	rows, err := doc.Rows(loader.RowsBlock{Selector: "item"})
	if err != nil || len(rows) != 1 {
		t.Fatalf("rows = %d err = %v, want 1", len(rows), err)
	}

	tests := []struct {
		name  string
		block loader.SelectorBlock
		want  string
	}{
		{
			// The def's original casing, as vendored defs author it.
			name:  "mixed-case element selector",
			block: loader.SelectorBlock{Selector: "pubDate"},
			want:  "Mon, 02 Jan 2006 15:04:05 -0700",
		},
		{
			name:  "lowercase element selector",
			block: loader.SelectorBlock{Selector: "pubdate"},
			want:  "Mon, 02 Jan 2006 15:04:05 -0700",
		},
		{
			// [contentLength] compiles to key "contentlength"; the attribute
			// VALUE (the URL) keeps its case.
			name:  "mixed-case attribute key in selector, value case preserved",
			block: loader.SelectorBlock{Selector: "enclosure[contentLength]", Attribute: "url"},
			want:  "https://xml.test/dl/MixedCase.torrent",
		},
		{
			// Qualified names lowercase the prefix too; the value keeps case.
			name:  "mixed-case namespaced element",
			block: loader.SelectorBlock{Selector: `tz\:info[name="Seeders"]`, Attribute: "value"},
			want:  "42",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, found, err := New().Field(rows[0], tt.block, nil)
			if err != nil || !found {
				t.Fatalf("Field(%q): found=%v err=%v", tt.block.Selector, found, err)
			}
			if got != tt.want {
				t.Errorf("Field(%q) = %q, want %q", tt.block.Selector, got, tt.want)
			}
		})
	}
}

// TestParseXMLInvalid proves malformed XML degrades cleanly (a loud error, no
// panic).
func TestParseXMLInvalid(t *testing.T) {
	t.Parallel()
	if _, err := New().ParseXML([]byte("<rss><channel><item></rss")); err == nil {
		t.Fatal("ParseXML of malformed XML = nil error, want a loud error")
	}
}

// TestParseXMLCDATA proves CDATA sections round-trip as literal text (the '&' and
// '<...>' inside are content, not entities/markup), and that text abutting a CDATA
// section concatenates — including for a :contains selector spanning the boundary,
// matching how AngleSharp exposes the element's text content.
func TestParseXMLCDATA(t *testing.T) {
	t.Parallel()
	const feed = `<rss><channel><item>
  <title><![CDATA[Title & <Raw> Markup]]></title>
  <desc>Pre <![CDATA[Mid]]> Post</desc>
</item></channel></rss>`
	doc, err := New().ParseXML([]byte(feed))
	if err != nil {
		t.Fatalf("ParseXML: %v", err)
	}
	rows, err := doc.Rows(loader.RowsBlock{Selector: "item"})
	if err != nil || len(rows) != 1 {
		t.Fatalf("rows = %d err = %v, want 1", len(rows), err)
	}

	title, found, err := New().Field(rows[0], loader.SelectorBlock{Selector: "title"}, nil)
	if err != nil || !found {
		t.Fatalf("title: found=%v err=%v", found, err)
	}
	if title != "Title & <Raw> Markup" {
		t.Errorf("CDATA title = %q, want literal %q", title, "Title & <Raw> Markup")
	}

	desc, found, err := New().Field(rows[0], loader.SelectorBlock{Selector: "desc"}, nil)
	if err != nil || !found {
		t.Fatalf("desc: found=%v err=%v", found, err)
	}
	if desc != "Pre Mid Post" {
		t.Errorf("text spanning CDATA = %q, want %q", desc, "Pre Mid Post")
	}

	// :contains across the text/CDATA boundary uses the concatenated text content.
	hit, err := doc.Rows(loader.RowsBlock{Selector: `desc:contains("Pre Mid Post")`})
	if err != nil {
		t.Fatalf(":contains query: %v", err)
	}
	if len(hit) != 1 {
		t.Errorf(":contains across CDATA boundary matched %d, want 1", len(hit))
	}
}

// TestParseXMLComments proves XML comments are dropped (not exposed as text), so an
// element's text content and a :contains selector see only the real character data,
// matching AngleSharp's selectable output (where a comment is a non-text node).
func TestParseXMLComments(t *testing.T) {
	t.Parallel()
	const feed = `<rss><channel><item><title>Real<!-- hidden -->Text</title></item></channel></rss>`
	doc, err := New().ParseXML([]byte(feed))
	if err != nil {
		t.Fatalf("ParseXML: %v", err)
	}
	rows, err := doc.Rows(loader.RowsBlock{Selector: "item"})
	if err != nil || len(rows) != 1 {
		t.Fatalf("rows = %d err = %v, want 1", len(rows), err)
	}
	title, found, err := New().Field(rows[0], loader.SelectorBlock{Selector: "title"}, nil)
	if err != nil || !found {
		t.Fatalf("title: found=%v err=%v", found, err)
	}
	if title != "RealText" {
		t.Errorf("title = %q, want %q (comment dropped, text concatenated)", title, "RealText")
	}
	hidden, err := doc.Rows(loader.RowsBlock{Selector: `title:contains("hidden")`})
	if err != nil {
		t.Fatalf(":contains query: %v", err)
	}
	if len(hidden) != 0 {
		t.Errorf("comment text was selectable via :contains (%d hits), want 0", len(hidden))
	}
}

// TestParseXMLDefaultNamespace proves an element in a default namespace (xmlns=...)
// is selectable by its bare local name, the way Jackett's selectors reference an
// Atom feed's elements.
func TestParseXMLDefaultNamespace(t *testing.T) {
	t.Parallel()
	const feed = `<feed xmlns="http://www.w3.org/2005/Atom">
  <entry><title>Atom Title</title></entry>
</feed>`
	doc, err := New().ParseXML([]byte(feed))
	if err != nil {
		t.Fatalf("ParseXML: %v", err)
	}
	rows, err := doc.Rows(loader.RowsBlock{Selector: "feed > entry"})
	if err != nil || len(rows) != 1 {
		t.Fatalf("rows = %d err = %v, want 1", len(rows), err)
	}
	title, found, err := New().Field(rows[0], loader.SelectorBlock{Selector: "title"}, nil)
	if err != nil || !found {
		t.Fatalf("title: found=%v err=%v", found, err)
	}
	if title != "Atom Title" {
		t.Errorf("default-namespace title = %q, want %q", title, "Atom Title")
	}
}

// TestParseXMLUndeclaredPrefix proves an undeclared namespace prefix degrades
// cleanly: harbrr parses leniently (encoding/xml Strict=false) and keeps the literal
// qualified name, so a `foo\:bar` selector still matches — the same qualified-name
// selection harbrr (and Jackett's defs) use for all namespaced elements. Jackett's
// default `new XmlParser()` is also lenient here (it does not reject an undeclared
// prefix), so this is a robustness property, not a parity divergence. No corpus def
// selects such an element by a bare local name, so only the qualified form is pinned.
func TestParseXMLUndeclaredPrefix(t *testing.T) {
	t.Parallel()
	const feed = `<rss><channel><item><foo:bar>x</foo:bar></item></channel></rss>`
	doc, err := New().ParseXML([]byte(feed))
	if err != nil {
		t.Fatalf("ParseXML of an undeclared prefix should degrade, not error: %v", err)
	}
	val, found, err := New().Field(doc.Root(), loader.SelectorBlock{Selector: `foo\:bar`}, nil)
	if err != nil || !found {
		t.Fatalf("foo:bar: found=%v err=%v", found, err)
	}
	if val != "x" {
		t.Errorf("foo:bar = %q, want x", val)
	}
}
