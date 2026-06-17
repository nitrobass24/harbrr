package iptorrents

import (
	"math"
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

// coerceInt extracts the leading integer from s, reproducing ParseUtil.CoerceInt's
// lenient parse: the first run of digits (optionally signed) is returned, else 0.
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
// strips (#6582).
var titleControlChars = regexp.MustCompile(`[\x00-\x08\x0A-\x1F\x{0100}-\x{FFFF}]`)

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

// parseSizeBytes reproduces Jackett/Prowlarr's ParseUtil.GetBytes: the numeric part
// keeps digits/'.'/',' (',' -> '.', extra '.' are thousands separators dropped), the
// unit is the letters only with 'i' stripped, and the byte count is value*multiplier
// computed and truncated in float32 (KB→MB→GB→TB by Contains). An unrecognised unit is
// a raw byte count; empty/"-" coerce to 0.
func parseSizeBytes(s string) int64 {
	val := coerceFloat32(normalizeDecimal(keepNumeric(s)))
	unit := strings.ReplaceAll(strings.ToLower(lettersOnly(s)), "i", "")
	const step float32 = 1024
	switch {
	case strings.Contains(unit, "kb"):
		return truncFloat32(val * step)
	case strings.Contains(unit, "mb"):
		return truncFloat32(val * step * step)
	case strings.Contains(unit, "gb"):
		return truncFloat32(val * step * step * step)
	case strings.Contains(unit, "tb"):
		return truncFloat32(val * step * step * step * step)
	default:
		return truncFloat32(val)
	}
}

// keepNumeric keeps only digits, '.' and ',' from s (GetBytes's pre-CoerceFloat scan).
func keepNumeric(s string) string {
	return strings.Map(func(r rune) rune {
		if (r >= '0' && r <= '9') || r == '.' || r == ',' {
			return r
		}
		return -1
	}, s)
}

// normalizeDecimal maps ',' to '.', then treats all but the last '.' as thousands
// separators and drops them (so "1.018,29" -> "1018.29").
func normalizeDecimal(s string) string {
	s = strings.ReplaceAll(s, ",", ".")
	if strings.Count(s, ".") <= 1 {
		return s
	}
	last := strings.LastIndex(s, ".")
	return strings.ReplaceAll(s[:last], ".", "") + s[last:]
}

// lettersOnly returns the ASCII letters of s (GetBytes's unit extraction).
func lettersOnly(s string) string {
	return strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
			return r
		}
		return -1
	}, s)
}

// coerceFloat32 parses s as a float32, returning 0 on empty/unparseable input (CoerceFloat).
func coerceFloat32(s string) float32 {
	if s == "" {
		return 0
	}
	f, err := strconv.ParseFloat(s, 32)
	if err != nil {
		return 0
	}
	return float32(f)
}

// truncFloat32 truncates a float32 byte count toward zero into an int64, clamping
// non-finite/overflowing values rather than panicking (the C# (long) cast).
func truncFloat32(v float32) int64 {
	t := math.Trunc(float64(v))
	switch {
	case math.IsNaN(t) || t <= math.MinInt64:
		return 0
	case t >= math.MaxInt64:
		return math.MaxInt64
	default:
		return int64(t)
	}
}
