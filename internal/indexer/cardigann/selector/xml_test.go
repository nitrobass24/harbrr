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

	title, found, err := New().Field(rows[0], loader.SelectorBlock{Selector: "title"})
	if err != nil || !found {
		t.Fatalf("title: found=%v err=%v", found, err)
	}
	if title != "First Release" {
		t.Errorf("title = %q, want First Release", title)
	}

	// <link> round-trips as text (the HTML5 parser would treat it as void and
	// the URL would leak out as a sibling).
	link, found, err := New().Field(rows[0], loader.SelectorBlock{Selector: "link"})
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
	})
	if err != nil || !found {
		t.Fatalf("torznab:attr seeders: found=%v err=%v", found, err)
	}
	if seeders != "42" {
		t.Errorf("seeders = %q, want 42", seeders)
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
