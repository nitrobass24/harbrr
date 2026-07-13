package search

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/loader"
)

// fb is a terse FilterBlock constructor for the table tests.
func fb(name string, args ...string) loader.FilterBlock {
	return loader.FilterBlock{Name: name, Args: args}
}

func TestApplyStringFilters(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		in      string
		filters []loader.FilterBlock
		want    string
		wantErr bool
	}{
		{name: "append", in: "abc", filters: []loader.FilterBlock{fb("append", "X")}, want: "abcX"},
		{name: "prepend", in: "abc", filters: []loader.FilterBlock{fb("prepend", "X")}, want: "Xabc"},
		{name: "replace all", in: "a.b.c", filters: []loader.FilterBlock{fb("replace", ".", "-")}, want: "a-b-c"},
		{name: "replace missing arg", in: "x", filters: []loader.FilterBlock{fb("replace", ".")}, wantErr: true},
		{name: "trim whitespace default", in: "  hi  ", filters: []loader.FilterBlock{fb("trim")}, want: "hi"},
		{name: "trim cutset first rune", in: "xxhixx", filters: []loader.FilterBlock{fb("trim", "x")}, want: "hi"},
		{name: "trim empty arg is whitespace", in: " a ", filters: []loader.FilterBlock{fb("trim", "")}, want: "a"},
		{name: "tolower", in: "AbC", filters: []loader.FilterBlock{fb("tolower")}, want: "abc"},
		{name: "toupper", in: "AbC", filters: []loader.FilterBlock{fb("toupper")}, want: "ABC"},
		{name: "urldecode plus", in: "a+b%20c", filters: []loader.FilterBlock{fb("urldecode")}, want: "a b c"},
		// WebUtility.UrlDecode leniency (vs url.QueryUnescape, which errors on any
		// invalid escape): malformed percent sequences stay literal, never error.
		// Real-world trigger: querystring-then-urldecode chains (e.g. aftershock)
		// feed already-decoded titles like "100% FLAC" back through urldecode.
		{name: "urldecode bare percent literal", in: "100% FLAC", filters: []loader.FilterBlock{fb("urldecode")}, want: "100% FLAC"},
		{name: "urldecode valid escape and plus", in: "100%25+FLAC", filters: []loader.FilterBlock{fb("urldecode")}, want: "100% FLAC"},
		{name: "urldecode multibyte utf8", in: "%E2%80%A6", filters: []loader.FilterBlock{fb("urldecode")}, want: "…"},
		{name: "urldecode lowercase hex", in: "%2f", filters: []loader.FilterBlock{fb("urldecode")}, want: "/"},
		{name: "urldecode uppercase hex", in: "%2F", filters: []loader.FilterBlock{fb("urldecode")}, want: "/"},
		{name: "urldecode trailing percent", in: "abc%", filters: []loader.FilterBlock{fb("urldecode")}, want: "abc%"},
		{name: "urldecode one hex digit then end", in: "abc%2", filters: []loader.FilterBlock{fb("urldecode")}, want: "abc%2"},
		{name: "urldecode non-hex escape", in: "a%GGb", filters: []loader.FilterBlock{fb("urldecode")}, want: "a%GGb"},
		{name: "urldecode percent before valid escape", in: "%%41", filters: []loader.FilterBlock{fb("urldecode")}, want: "%A"},
		{name: "urlencode space", in: "a b", filters: []loader.FilterBlock{fb("urlencode")}, want: "a+b"},
		{name: "htmldecode", in: "a&amp;b&lt;c", filters: []loader.FilterBlock{fb("htmldecode")}, want: "a&b<c"},
		{name: "htmlencode", in: "a&b<c", filters: []loader.FilterBlock{fb("htmlencode")}, want: "a&amp;b&lt;c"},
		// WebUtility.HtmlEncode fidelity (vs Go html.EscapeString): " -> &quot; (not &#34;),
		// ' -> &#39;, > -> &gt;; Latin-1 (é U+00E9) -> &#233;; BMP >= U+0100 (中) and the
		// U+007F–U+009F band pass through; astral (😀 U+1F600) -> &#128512;.
		{name: "htmlencode quotes", in: `"x'>`, filters: []loader.FilterBlock{fb("htmlencode")}, want: "&quot;x&#39;&gt;"},
		{name: "htmlencode latin1", in: "café", filters: []loader.FilterBlock{fb("htmlencode")}, want: "caf&#233;"},
		{name: "htmlencode bmp passthrough", in: "中", filters: []loader.FilterBlock{fb("htmlencode")}, want: "中"},
		{name: "htmlencode astral", in: "😀", filters: []loader.FilterBlock{fb("htmlencode")}, want: "&#128512;"},
		{name: "hexdump passthrough", in: "keep", filters: []loader.FilterBlock{fb("hexdump")}, want: "keep"},
		{name: "strdump passthrough", in: "keep", filters: []loader.FilterBlock{fb("strdump", "tag")}, want: "keep"},
		{
			name: "split positive index", in: "a/b/c",
			filters: []loader.FilterBlock{fb("split", "/", "1")}, want: "b",
		},
		{
			name: "split negative index", in: "a/b/c",
			filters: []loader.FilterBlock{fb("split", "/", "-1")}, want: "c",
		},
		{
			name: "split out of range", in: "a/b",
			filters: []loader.FilterBlock{fb("split", "/", "9")}, wantErr: true,
		},
		{
			name: "split missing arg", in: "a/b",
			filters: []loader.FilterBlock{fb("split", "/")}, wantErr: true,
		},
		{
			name: "querystring named param", in: "https://x.test/dl?passkey=ABC&id=7",
			filters: []loader.FilterBlock{fb("querystring", "id")}, want: "7",
		},
		{
			name: "querystring fragment dropped", in: "x?id=7#id=9",
			filters: []loader.FilterBlock{fb("querystring", "id")}, want: "7",
		},
		{
			name: "querystring absent param empty", in: "x?id=7",
			filters: []loader.FilterBlock{fb("querystring", "nope")}, want: "",
		},
		{
			name: "querystring no query errors", in: "https://x.test/dl",
			filters: []loader.FilterBlock{fb("querystring", "id")}, wantErr: true,
		},
		// Jackett's QueryHelpers.ParseQuery never throws; Go's url.ParseQuery does,
		// which used to discard its partial result and drop the whole row. A sibling
		// param with a bare '%' must not lose the target value.
		{
			name: "querystring sibling bad percent", in: "x?id=7&note=50%",
			filters: []loader.FilterBlock{fb("querystring", "id")}, want: "7",
		},
		// ';' is data, not a separator: Jackett splits on '&' only, so the whole
		// "1;b=2" is param a's value (Go's url.ParseQuery drops any ';'-bearing pair).
		{
			name: "querystring semicolon is data target sibling", in: "x?a=1;b=2&id=9",
			filters: []loader.FilterBlock{fb("querystring", "id")}, want: "9",
		},
		{
			name: "querystring semicolon is data target self", in: "x?a=1;b=2&id=9",
			filters: []loader.FilterBlock{fb("querystring", "a")}, want: "1;b=2",
		},
		{
			name: "querystring first occurrence wins", in: "x?id=7&id=9",
			filters: []loader.FilterBlock{fb("querystring", "id")}, want: "7",
		},
		// Decoded like Jackett: '+' -> space, valid %XX applied to key and value.
		{
			name: "querystring value plus and escape", in: "x?title=a+b%2Fc",
			filters: []loader.FilterBlock{fb("querystring", "title")}, want: "a b/c",
		},
		{
			name: "querystring encoded key matched", in: "x?a%62=hit",
			filters: []loader.FilterBlock{fb("querystring", "ab")}, want: "hit",
		},
		{
			name: "validfilename strips invalid", in: `a/b:c*d?e`,
			filters: []loader.FilterBlock{fb("validfilename")}, want: "a_b_c_d_e",
		},
		{
			name: "validfilename clean passes through", in: "clean name",
			filters: []loader.FilterBlock{fb("validfilename")}, want: "clean name",
		},
		{
			name: "validfilename all invalid each replaced", in: `///`,
			filters: []loader.FilterBlock{fb("validfilename")}, want: "___",
		},
		{
			name: "diacritics replace", in: "Café Niño",
			filters: []loader.FilterBlock{fb("diacritics", "replace")}, want: "Cafe Nino",
		},
		{
			name: "diacritics bad arg errors", in: "x",
			filters: []loader.FilterBlock{fb("diacritics", "strip")}, wantErr: true,
		},
	}

	r := NewFilterRegistry()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := r.Apply(tc.in, tc.filters)
			assertResult(t, got, err, tc.want, tc.wantErr)
		})
	}
}

func TestApplyRegexFilters(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		in      string
		filters []loader.FilterBlock
		want    string
		wantErr bool
	}{
		{
			name: "regexp first group", in: "Size: 1.5 GB",
			filters: []loader.FilterBlock{fb("regexp", `Size: ([\d.]+)`)}, want: "1.5",
		},
		{
			name: "regexp no match empty", in: "nothing",
			filters: []loader.FilterBlock{fb("regexp", `(\d+)`)}, want: "",
		},
		{
			name: "regexp no capture group empty", in: "abc123",
			filters: []loader.FilterBlock{fb("regexp", `\d+`)}, want: "",
		},
		{
			name: "re_replace backref", in: "2024-01-02",
			filters: []loader.FilterBlock{fb("re_replace", `(\d+)-(\d+)-(\d+)`, `$3/$2/$1`)}, want: "02/01/2024",
		},
		{
			name: "re_replace strip", in: "a1b2c3",
			filters: []loader.FilterBlock{fb("re_replace", `\d`, ``)}, want: "abc",
		},
		{
			name: "re_replace missing arg", in: "x",
			filters: []loader.FilterBlock{fb("re_replace", `\d`)}, wantErr: true,
		},
		{
			name: "re_replace bad pattern", in: "x",
			filters: []loader.FilterBlock{fb("re_replace", `(`, ``)}, wantErr: true,
		},
		{
			name: "validate intersect lowercased", in: "Action.Comedy.Drama",
			filters: []loader.FilterBlock{fb("validate", "comedy,drama,horror")}, want: "comedy,drama",
		},
		{
			name: "validate no match empty", in: "Action",
			filters: []loader.FilterBlock{fb("validate", "comedy")}, want: "",
		},
	}

	r := NewFilterRegistry()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := r.Apply(tc.in, tc.filters)
			assertResult(t, got, err, tc.want, tc.wantErr)
		})
	}
}

func TestJSONJoinArray(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		in      string
		filters []loader.FilterBlock
		want    string
		wantErr bool
	}{
		{
			name: "join string array", in: `{"tags":["a","b","c"]}`,
			filters: []loader.FilterBlock{fb("jsonjoinarray", "tags", ", ")}, want: "a, b, c",
		},
		{
			name: "join numbers", in: `{"ids":[1,2,3]}`,
			filters: []loader.FilterBlock{fb("jsonjoinarray", "ids", "-")}, want: "1-2-3",
		},
		{
			name: "dollar path prefix", in: `{"x":{"y":["p","q"]}}`,
			filters: []loader.FilterBlock{fb("jsonjoinarray", "$.x.y", "/")}, want: "p/q",
		},
		{
			name: "non array errors", in: `{"x":"y"}`,
			filters: []loader.FilterBlock{fb("jsonjoinarray", "x", ",")}, wantErr: true,
		},
		{
			name: "bad json errors", in: `not json`,
			filters: []loader.FilterBlock{fb("jsonjoinarray", "x", ",")}, wantErr: true,
		},
	}

	r := NewFilterRegistry()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := r.Apply(tc.in, tc.filters)
			assertResult(t, got, err, tc.want, tc.wantErr)
		})
	}
}

func TestChaining(t *testing.T) {
	t.Parallel()

	r := NewFilterRegistry()
	// trim -> tolower -> replace, threaded left-to-right.
	got, err := r.Apply("  HELLO WORLD  ", []loader.FilterBlock{
		fb("trim"), fb("tolower"), fb("replace", " ", "_"),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "hello_world" {
		t.Fatalf("got %q, want %q", got, "hello_world")
	}
}

func TestUnknownFilterIsLoud(t *testing.T) {
	t.Parallel()

	r := NewFilterRegistry()
	_, err := r.Apply("v", []loader.FilterBlock{fb("not_a_filter")})
	if err == nil {
		t.Fatal("expected error for unknown filter, got nil")
	}
	if !strings.Contains(err.Error(), "not_a_filter") {
		t.Fatalf("error should name the unknown filter, got %q", err.Error())
	}
}

func TestDateFiltersDefaultUnwired(t *testing.T) {
	t.Parallel()

	r := NewFilterRegistry()
	for _, name := range []string{"dateparse", "timeparse", "timeago", "reltime", "fuzzytime"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, err := r.Apply("2024-01-02", []loader.FilterBlock{fb(name, "yyyy-MM-dd")})
			if !errors.Is(err, errDateUnwired) {
				t.Fatalf("%s: expected errDateUnwired, got %v", name, err)
			}
		})
	}
}

func TestDateFiltersNilSeamIsLoud(t *testing.T) {
	t.Parallel()

	// A caller reassigning a date seam to nil must surface the loud
	// errDateUnwired, never panic on a nil call.
	r := NewFilterRegistry()
	r.ParseDate = nil
	r.ParseRelTime = nil
	for _, name := range []string{"dateparse", "timeparse", "timeago", "reltime", "fuzzytime"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, err := r.Apply("2024-01-02", []loader.FilterBlock{fb(name, "yyyy-MM-dd")})
			if !errors.Is(err, errDateUnwired) {
				t.Fatalf("%s: expected errDateUnwired, got %v", name, err)
			}
		})
	}
}

func TestDateFiltersInjectedDispatch(t *testing.T) {
	t.Parallel()

	var gotValue, gotLayout string
	r := NewFilterRegistry()
	r.ParseDate = func(value, layout string) (string, error) {
		gotValue, gotLayout = value, layout
		return "PARSED", nil
	}
	r.ParseRelTime = func(value string) (string, error) {
		return "REL:" + value, nil
	}

	// append " +02:00" then dateparse: confirms chaining feeds the date op the
	// post-append value and the layout from the filter args.
	got, err := r.Apply("2024-01-02 13:00", []loader.FilterBlock{
		fb("append", " +02:00"),
		fb("dateparse", "yyyy-MM-dd HH:mm zzz"),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "PARSED" {
		t.Fatalf("got %q, want PARSED", got)
	}
	if gotValue != "2024-01-02 13:00 +02:00" {
		t.Fatalf("ParseDate value = %q, want post-append value", gotValue)
	}
	if gotLayout != "yyyy-MM-dd HH:mm zzz" {
		t.Fatalf("ParseDate layout = %q", gotLayout)
	}

	rel, err := r.Apply("2 hours ago", []loader.FilterBlock{fb("timeago")})
	if err != nil {
		t.Fatalf("unexpected reltime error: %v", err)
	}
	if rel != "REL:2 hours ago" {
		t.Fatalf("reltime dispatch = %q", rel)
	}
}

func TestDateFilterPropagatesInjectedError(t *testing.T) {
	t.Parallel()

	sentinel := errors.New("boom")
	r := NewFilterRegistry()
	r.ParseDate = func(string, string) (string, error) { return "", sentinel }

	_, err := r.Apply("x", []loader.FilterBlock{fb("dateparse", "L")})
	if !errors.Is(err, sentinel) {
		t.Fatalf("expected wrapped sentinel, got %v", err)
	}
}

func TestRowFilters(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		title    string
		keywords string
		want     bool
	}{
		{name: "all tokens present", title: "The Matrix 1999 1080p", keywords: "matrix 1999", want: true},
		{name: "missing token", title: "The Matrix 1999", keywords: "matrix 2003", want: false},
		{name: "case insensitive", title: "BIG BUCK BUNNY", keywords: "buck bunny", want: true},
		{name: "stopwords ignored", title: "Matrix", keywords: "the matrix and an", want: true},
		{name: "short tokens ignored", title: "Matrix", keywords: "a matrix x", want: true},

		// Non-Latin AND-match: .NET's \w is Unicode-aware, so both Cyrillic/CJK
		// tokens must be present. RE2's ASCII \w used to tokenize these into
		// zero tokens, making andMatch always true (a superset vs Jackett) —
		// the "missing token" rows below fail-before / pass-after the fix.
		{name: "cyrillic both tokens present", title: "Война и мир 2007 BDRip", keywords: "война мир", want: true},
		{name: "cyrillic missing token", title: "Война 2007 BDRip", keywords: "война мир", want: false},
		{name: "cjk both tokens present", title: "三体 刘慈欣 2023 WEB-DL", keywords: "三体 刘慈欣", want: true},
		{name: "cjk missing token", title: "三体 2023 WEB-DL", keywords: "三体 刘慈欣", want: false},
		{name: "korean both tokens present", title: "오징어 게임 2021 1080p", keywords: "오징어 게임", want: true},
		{name: "korean missing token", title: "오징어 2021 1080p", keywords: "오징어 게임", want: false},
		{name: "vietnamese combining marks kept", title: "Mắt Biếc 2019 1080p", keywords: "mắt biếc", want: true},
		{name: "vietnamese missing token", title: "Mắt 2019 1080p", keywords: "mắt biếc", want: false},

		// Class-choice guards: these use the three general categories that
		// distinguish the exact .NET \w class [\p{L}\p{Mn}\p{Nd}\p{Pc}] from the
		// looser [^\pL\pN_] approximation. In each, treating the codepoint as a
		// separator (the approximation / RE2's ASCII \w) would shred the token
		// and flip the result — so they pin the exact class, not just Unicode.
		// \p{Mn}: a DECOMPOSED combining acute (U+0301) must stay in-token; as a
		// separator "éx" shreds to "e","x" (both dropped ≤1 rune), dropping
		// the requirement and wrongly keeping the row.
		{name: "combining mark keeps token (Mn)", title: "éx concert 2019", keywords: "éx concert", want: true},
		{name: "combining mark required token missing (Mn)", title: "concert 2019 1080p", keywords: "éx concert", want: false},
		// \p{Pc}: connector punctuation beyond '_' (U+203F ‿) is a word char in
		// .NET \w; the '_'-only approximation would split "a‿b" and drop it.
		{name: "connector punctuation required token missing (Pc)", title: "beta gamma 2019", keywords: "a‿b beta", want: false},
		// \p{No}: a fraction (½ U+00BD) is NOT a word char in .NET \w, so "12½"
		// splits to "12"; the \pN approximation would keep "12½" whole and miss.
		{name: "number-other splits the token (No)", title: "12 inch vinyl 2019", keywords: "12½ inch", want: true},

		// A single non-Latin char is one rune → dropped (Jackett's Length ≤ 1),
		// so it is not required and the row is kept even though the title lacks
		// that char.
		{name: "single cjk char dropped", title: "刘慈欣 novel", keywords: "三 novel", want: true},

		// Mixed Latin + non-Latin: every non-dropped token must be present.
		{name: "mixed both present", title: "三体 Remembrance 1080p", keywords: "三体 remembrance", want: true},
		{name: "mixed missing latin token", title: "三体 1080p", keywords: "三体 remembrance", want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := andMatch(tc.title, tc.keywords); got != tc.want {
				t.Fatalf("andMatch(%q,%q) = %v, want %v", tc.title, tc.keywords, got, tc.want)
			}
		})
	}

	if !strDump("anything") {
		t.Fatal("strDump should always retain the row")
	}
	if !rowFilterKnown("andmatch") || !rowFilterKnown("strdump") {
		t.Fatal("row filter names must be known")
	}
	if rowFilterKnown("bogus") {
		t.Fatal("bogus row filter must not be known")
	}
}

// TestCorpusFilterCompleteness is the headline gate: every field filter and row
// filter referenced by any vendored definition must be a name the registry
// knows. Zero unknown names are tolerated.
func TestCorpusFilterCompleteness(t *testing.T) {
	t.Parallel()

	defs, skipped, err := loader.New("").LoadAll()
	if err != nil {
		t.Fatalf("loading corpus: %v", err)
	}
	if len(defs) == 0 {
		t.Fatal("loaded zero definitions")
	}
	t.Logf("loaded %d definitions (%d skipped)", len(defs), len(skipped))

	r := NewFilterRegistry()
	fieldCounts := map[string]int{}
	rowCounts := map[string]int{}
	var unknown []string

	for _, def := range defs {
		walkDefFilters(def, func(name string, isRow bool) {
			if isRow {
				rowCounts[name]++
				if !rowFilterKnown(name) {
					unknown = append(unknown, "row:"+name+" in "+def.ID)
				}
				return
			}
			fieldCounts[name]++
			if !r.Known(name) {
				unknown = append(unknown, "field:"+name+" in "+def.ID)
			}
		})
	}

	logCounts(t, "field", fieldCounts)
	logCounts(t, "row", rowCounts)

	if len(unknown) > 0 {
		sort.Strings(unknown)
		t.Fatalf("unknown filter names in corpus (%d): %s", len(unknown), strings.Join(unknown, "; "))
	}
}

// walkDefFilters visits every field-filter name (SelectorBlock.Filters across
// fields, login selector inputs, download selectors, and the row/error/count/
// dateheader selectors) and every row-filter name (RowsBlock.Filters) referenced
// by def. The walk is exhaustive over the schema's filter-bearing locations so
// the completeness gate cannot miss a filter tucked on a rarely-used selector.
func walkDefFilters(def *loader.Definition, visit func(name string, isRow bool)) {
	for _, rf := range def.Search.Rows.Filters {
		visit(rf.Name, true)
	}
	for _, blocks := range fieldFilterBlocks(def) {
		for _, f := range blocks {
			visit(f.Name, false)
		}
	}
}

// appendSelectorFilters appends sb.Filters to out when sb is non-nil. Used to
// reach the filter-bearing selectors that hang off optional sub-blocks
// (dateheaders, count, error messages).
func appendSelectorFilters(out [][]loader.FilterBlock, sb *loader.SelectorBlock) [][]loader.FilterBlock {
	if sb == nil {
		return out
	}
	return append(out, sb.Filters)
}

// errorMessageFilters appends the message-selector filters of every ErrorBlock.
func errorMessageFilters(out [][]loader.FilterBlock, errs []loader.ErrorBlock) [][]loader.FilterBlock {
	for i := range errs {
		out = appendSelectorFilters(out, errs[i].Message)
	}
	return out
}

// fieldFilterBlocks collects every []FilterBlock a definition can carry, so the
// completeness walk sees field filters wherever the schema allows them.
func fieldFilterBlocks(def *loader.Definition) [][]loader.FilterBlock {
	var out [][]loader.FilterBlock

	out = append(out, def.Search.KeywordsFilters, def.Search.PreprocessingFilters)
	for _, fe := range def.Search.Fields.Ordered() {
		out = append(out, fe.Block.Filters)
	}
	out = appendSelectorFilters(out, def.Search.Rows.DateHeaders)
	out = appendSelectorFilters(out, def.Search.Rows.Count)
	out = errorMessageFilters(out, def.Search.Error)
	if login := def.Login; login != nil {
		for _, sb := range login.SelectorInputs {
			out = append(out, sb.Filters)
		}
		for _, sb := range login.GetSelectorInps {
			out = append(out, sb.Filters)
		}
		out = errorMessageFilters(out, login.Error)
	}
	if dl := def.Download; dl != nil {
		for _, sf := range dl.Selectors {
			out = append(out, sf.Filters)
		}
	}
	return out
}

func logCounts(t *testing.T, kind string, counts map[string]int) {
	t.Helper()
	names := make([]string, 0, len(counts))
	for n := range counts {
		names = append(names, n)
	}
	sort.Strings(names)
	var b strings.Builder
	for _, n := range names {
		fmt.Fprintf(&b, "\n  %-14s %d", n, counts[n])
	}
	t.Logf("%s filter usage:%s", kind, b.String())
}

func assertResult(t *testing.T, got string, err error, want string, wantErr bool) {
	t.Helper()
	if wantErr {
		if err == nil {
			t.Fatalf("expected error, got result %q", got)
		}
		return
	}
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}
