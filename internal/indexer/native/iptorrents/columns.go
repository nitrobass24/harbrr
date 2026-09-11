package iptorrents

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/PuerkitoBio/goquery"
)

// headerColumns returns the trimmed text of each `<th>` in the torrents table header,
// the basis for resolving stat columns by name (IPTorrentsParser).
func headerColumns(doc *goquery.Document) []string {
	var cols []string
	doc.Find(`table#torrents > thead > tr > th`).Each(func(_ int, th *goquery.Selection) {
		cols = append(cols, strings.TrimSpace(th.Text()))
	})
	return cols
}

// rowCellCount returns the `<td>` count of the first body row, used to pick Prowlarr's
// fallback stat offset: a 10-cell row offsets the grabs/seeders/leechers block to 7,
// otherwise 6 (IPTorrentsParser's `row.Children.Length == 10 ? 7 : 6`).
func rowCellCount(doc *goquery.Document) int {
	return doc.Find(`table#torrents > tbody > tr`).First().Children().Length()
}

// resolveColumns locates each stat column by its header text, falling back to Prowlarr's
// positional defaults when a header is absent. The grabs/seeders/leechers fallbacks
// advance from a base offset (7 for a 10-cell row, else 6), matching the post-increment
// chain in IPTorrentsParser.
func resolveColumns(headers []string, cellCount int) columnLayout {
	base := 6
	if cellCount == 10 {
		base = 7
	}
	return columnLayout{
		size:     findColumn(headers, "Sort by size", defaultSizeColumn),
		files:    findColumn(headers, "Sort by files", -1),
		grabs:    findColumn(headers, "Sort by snatches", base),
		seeders:  findColumn(headers, "Sort by seeders", base+1),
		leechers: findColumn(headers, "Sort by leechers", base+2),
	}
}

// findColumn returns the index of the header whose trimmed text equals name (ordinal
// match, like Prowlarr's FindColumnIndexOrDefault), else the default index.
func findColumn(headers []string, name string, def int) int {
	for i, h := range headers {
		if h == name {
			return i
		}
	}
	return def
}

// cellSet is a row's `<td>` children, indexed for stat extraction.
type cellSet struct{ sel *goquery.Selection }

func cells(row *goquery.Selection) cellSet { return cellSet{sel: row.Children()} }

// textAt returns the trimmed text of the cell at i, or "" when i is out of range
// (a missing/unresolved column).
func (c cellSet) textAt(i int) string {
	if i < 0 || i >= c.sel.Length() {
		return ""
	}
	return strings.TrimSpace(c.sel.Eq(i).Text())
}

// intAt coerces the cell text at i to an int64, matching ParseUtil.CoerceInt (digits
// only, 0 on no digits). The "files" cell carries a "Go to files" link label Prowlarr
// strips; coerceInt's digit-only scan drops it anyway.
func (c cellSet) intAt(i int) int64 { return coerceInt(c.textAt(i)) }

// coerceInt strips every non-digit rune from s and parses the concatenated digits,
// reproducing ParseUtil.CoerceInt's lenient parse (e.g. "1,234" -> 1234, "12 KB" ->
// 12); 0 when no digits remain. Used only for non-negative counts (seeders, leechers,
// files, grabs), so sign and digit-grouping are intentionally ignored.
func coerceInt(s string) int64 {
	s = strings.TrimSpace(s)
	digits := strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, s)
	if digits == "" {
		return 0
	}
	n, err := strconv.ParseInt(digits, 10, 64)
	if err != nil {
		return 0
	}
	return n
}

// titleControlChars matches the invalid control/high characters Prowlarr's CleanTitle
// strips. Prowlarr's class runs over UTF-16 code units, so its upper bound (U+FFFF)
// also catches both halves of every surrogate pair — i.e. every astral character. RE2
// matches code points, so the range runs to U+10FFFF to strip the same characters.
var titleControlChars = regexp.MustCompile(`[\x00-\x08\x0A-\x1F\x{0100}-\x{10FFFF}]`)

// titleRequestTag matches a bracketed REQ/REQUEST(ED) marker Prowlarr strips.
var titleRequestTag = regexp.MustCompile(`(?i)[\(\[\{]REQ(UEST(ED)?)?[\)\]\}]`)

// cleanTitle reproduces IPTorrentsParser.CleanTitle: drop stray control chars, drop a
// bracketed REQUEST marker, then trim surrounding spaces, dashes and colons. The third
// Prowlarr regex (dropping a leading bracketed language group) uses .NET-only behaviour
// on a narrow case; it is omitted (see the README divergence note).
func cleanTitle(title string) string {
	title = titleControlChars.ReplaceAllString(title, "")
	title = titleRequestTag.ReplaceAllString(title, "")
	return strings.Trim(strings.TrimSpace(title), " -:")
}
