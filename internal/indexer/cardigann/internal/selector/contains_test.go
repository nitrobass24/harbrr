package selector

import (
	"testing"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/loader"
)

// TestRewriteContains pins the selector-text rewrite that makes :contains
// case-sensitive: every well-formed `:contains(x)` becomes `:matches(p)` with
// a literal-quoted pattern; everything else passes through byte-for-byte.
func TestRewriteContains(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "no contains untouched",
			in:   `div.result > a[href*="/torrents/"]:not(.ad)`,
			want: `div.result > a[href*="/torrents/"]:not(.ad)`,
		},
		{
			name: "double quoted argument",
			in:   `div:contains("VIP")`,
			want: `div:matches(VIP)`,
		},
		{
			// "/*/" is NOT a complete comment in cascadia (the closer search
			// starts after the "/*", so the trailing "/" never closes): the
			// leading "/" then makes the arg malformed, so cascadia rejects the
			// original and the rewriter must leave it untouched (not produce a
			// compilable :matches).
			name: "incomplete /*/ comment leaves arg untouched",
			in:   `div:contains( /*/ x)`,
			want: `div:contains( /*/ x)`,
		},
		{
			// Here "/*/ x */" IS a complete comment (closes at the later "*/"),
			// so the real arg is "VIP" and must be rewritten case-sensitively.
			name: "comment spanning to later close then arg",
			in:   `div:contains(/*/ x */"VIP")`,
			want: `div:matches(VIP)`,
		},
		{
			name: "single quoted argument",
			in:   `div:contains('VIP')`,
			want: `div:matches(VIP)`,
		},
		{
			name: "unquoted identifier argument",
			in:   `td:contains(Freeleech)`,
			want: `td:matches(Freeleech)`,
		},
		{
			name: "pseudo name is case-insensitive",
			in:   `div:CONTAINS("VIP")`,
			want: `div:matches(VIP)`,
		},
		{
			name: "regex metacharacters quoted",
			in:   `a:contains("*TCG*")`,
			want: `a:matches(\*TCG\*)`,
		},
		{
			name: "parens hex-escaped for cascadia's raw regex scan",
			in:   `a:contains("(HD)")`,
			want: `a:matches(\x28HD\x29)`,
		},
		{
			name: "brackets hex-escaped for cascadia's raw regex scan",
			in:   `a:contains("[2160p]")`,
			want: `a:matches(\x5B2160p\x5D)`,
		},
		{
			name: "spaces hex-escaped so consumeParenthesis cannot eat them",
			in:   `td:contains(" at ")`,
			want: `td:matches(\x20at\x20)`,
		},
		{
			name: "css hex escape in identifier argument",
			in:   `span:contains(\00a0MB)`,
			want: "span:matches(\u00a0MB)",
		},
		{
			name: "nested inside has",
			in:   `tr:has(td:contains("Free"))`,
			want: `tr:has(td:matches(Free))`,
		},
		{
			name: "nested inside not",
			in:   `a:not(:contains("REMUX"))`,
			want: `a:not(:matches(REMUX))`,
		},
		{
			name: "multi-contains compound",
			in:   `a:contains("Films"):contains("Bluray Remux")`,
			want: `a:matches(Films):matches(Bluray\x20Remux)`,
		},
		{
			name: "quoted attribute value is not structure",
			in:   `a[title="see :contains(x)"]:contains("VIP")`,
			want: `a[title="see :contains(x)"]:matches(VIP)`,
		},
		{
			name: "existing matches regex copied verbatim",
			in:   `div:matches(fo(o)?px):contains("X")`,
			want: `div:matches(fo(o)?px):matches(X)`,
		},
		{
			name: "containsown untouched",
			in:   `div:containsown("x")`,
			want: `div:containsown("x")`,
		},
		{
			name: "empty argument untouched (always true either way)",
			in:   `div:contains("")`,
			want: `div:contains("")`,
		},
		{
			name: "malformed unquoted multi-word argument untouched",
			in:   `free_button:contains(Only Upload)`,
			want: `free_button:contains(Only Upload)`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := rewriteContains(tc.in); got != tc.want {
				t.Fatalf("rewriteContains(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestContainsCaseSensitiveHTML proves :contains now matches AngleSharp's
// case-SENSITIVE ordinal TextContent.Contains — including nested inside :has —
// where cascadia's built-in :contains matched case-insensitively (a strict
// superset). Each row selector is counted against a document holding both the
// exact-case and the wrong-case variant.
func TestContainsCaseSensitiveHTML(t *testing.T) {
	t.Parallel()

	const page = `<table>
		<tr class="vip"><td>a VIP badge</td></tr>
		<tr class="vip-lower"><td>a vip badge</td></tr>
		<tr class="free"><td><span>Freeleech!</span></td></tr>
		<tr class="free-lower"><td><span>freeleech!</span></td></tr>
		<tr class="split"><td><b>V</b>IP over descendants</td></tr>
	</table>`

	tests := []struct {
		name     string
		selector string
		wantRows int
	}{
		{
			name:     "exact case matches",
			selector: `td:contains("VIP")`,
			wantRows: 2, // .vip and the descendant-split .split cell
		},
		{
			name:     "lowercase literal matches only lowercase text",
			selector: `td:contains("vip")`,
			wantRows: 1, // .vip-lower only; was 3 with case-insensitive :contains
		},
		{
			name:     "nested inside has respects case",
			selector: `tr:has(td:contains("Freeleech"))`,
			wantRows: 1, // .free only; was 2
		},
		{
			name:     "textcontent concatenates descendant text",
			selector: `td:contains("VIP over")`,
			wantRows: 1, // .split: "V" lives in <b>, "IP over" in a sibling text node
		},
		{
			name:     "space-only argument still matches literal spaces",
			selector: `td:contains(" over ")`,
			wantRows: 1,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			doc, err := New().ParseHTML([]byte(page))
			if err != nil {
				t.Fatal(err)
			}
			rows, err := doc.Rows(loader.RowsBlock{Selector: tc.selector})
			if err != nil {
				t.Fatalf("Rows(%q): %v", tc.selector, err)
			}
			if len(rows) != tc.wantRows {
				t.Fatalf("Rows(%q) matched %d, want %d", tc.selector, len(rows), tc.wantRows)
			}
		})
	}
}

// TestContainsCaseBlockArm reproduces the wihd.yml category hazard as a
// case-SENSITIVITY check: the first-defined arm is a case-mismatched near-miss
// (capital "Bluray Remux") that must NOT match the lowercase page text. With
// cascadia's old case-insensitive :contains the first arm matched anyway and
// returned the wrong category id; case-sensitive matching skips it and the
// second arm (whose literal matches exactly) wins. Arm order is preserved via
// the ordered CaseBlock so the first-defined arm is genuinely tested first.
func TestContainsCaseBlockArm(t *testing.T) {
	t.Parallel()

	const page = `<div id="row"><a href="/torrents/1">Films / Bluray remux 4K</a></div>`
	doc, err := New().ParseHTML([]byte(page))
	if err != nil {
		t.Fatal(err)
	}
	rows, err := doc.Rows(loader.RowsBlock{Selector: "div#row"})
	if err != nil || len(rows) != 1 {
		t.Fatalf("rows err=%v n=%d", err, len(rows))
	}

	block := loader.SelectorBlock{
		Selector: "div#row",
		Case: loader.NewCaseBlock(
			loader.CaseEntry{Key: `a:contains("Films"):contains("Bluray Remux")`, Value: loader.Scalar{Value: "hd-remux", Set: true}},
			loader.CaseEntry{Key: `a:contains("Films"):contains("Bluray remux 4K")`, Value: loader.Scalar{Value: "uhd-remux", Set: true}},
		),
	}
	v, found, err := New().Field(rows[0], block, nil)
	if err != nil || !found {
		t.Fatalf("case field err=%v found=%v", err, found)
	}
	if v != "uhd-remux" {
		t.Fatalf("case arm = %q, want uhd-remux (case-insensitive :contains would pick hd-remux)", v)
	}
}

// TestCaseBlockDeclaredOrderFirstMatchWins pins the U1-F4a parity fix: when a
// cell satisfies TWO case arms, Jackett returns the FIRST-DEFINED arm's value
// (its Case dictionary iterates in document order, break-on-first-match). This
// is the exact wihd.yml shape: a "Films / Bluray Remux 4K" cell matches both the
// substring `:contains("Bluray Remux")` arm (defined first → Movies/HD) and the
// `:contains("Bluray Remux 4K")` arm (defined later → Movies/UHD). Declared
// order must pick HD. The retired orderedCases byte-sort put "Bluray Remux 4K"
// (space 0x20) before "Bluray Remux\"" (0x22) and wrongly returned UHD.
func TestCaseBlockDeclaredOrderFirstMatchWins(t *testing.T) {
	t.Parallel()

	const page = `<div id="row"><a href="/torrents/1">Films / Bluray Remux 4K</a></div>`
	doc, err := New().ParseHTML([]byte(page))
	if err != nil {
		t.Fatal(err)
	}
	rows, err := doc.Rows(loader.RowsBlock{Selector: "div#row"})
	if err != nil || len(rows) != 1 {
		t.Fatalf("rows err=%v n=%d", err, len(rows))
	}

	block := loader.SelectorBlock{
		Selector: "div#row",
		Case: loader.NewCaseBlock(
			loader.CaseEntry{Key: `a:contains("Films"):contains("Bluray Remux")`, Value: loader.Scalar{Value: "movies-hd", Set: true}},
			loader.CaseEntry{Key: `a:contains("Films"):contains("Bluray Remux 4K")`, Value: loader.Scalar{Value: "movies-uhd", Set: true}},
		),
	}
	v, found, err := New().Field(rows[0], block, nil)
	if err != nil || !found {
		t.Fatalf("case field err=%v found=%v", err, found)
	}
	if v != "movies-hd" {
		t.Fatalf("case arm = %q, want movies-hd (first-defined matching arm; byte-sort would pick movies-uhd)", v)
	}
}

// TestCaseBlockStarIsPositional proves "*" is a positional arm, not a deferred
// default: Jackett tests it inline (selection.Matches("*")) in document order and
// breaks on first match, so a "*" authored BEFORE a specific arm wins over that
// arm. The retired orderedCases forced "*" last and would have returned the
// specific arm here.
func TestCaseBlockStarIsPositional(t *testing.T) {
	t.Parallel()

	const page = `<div id="row"><span class="freeleech">FL</span></div>`
	doc, err := New().ParseHTML([]byte(page))
	if err != nil {
		t.Fatal(err)
	}
	rows, err := doc.Rows(loader.RowsBlock{Selector: "div#row"})
	if err != nil || len(rows) != 1 {
		t.Fatalf("rows err=%v n=%d", err, len(rows))
	}

	block := loader.SelectorBlock{
		Selector: "div#row",
		Case: loader.NewCaseBlock(
			loader.CaseEntry{Key: "*", Value: loader.Scalar{Value: "star", Set: true}},
			loader.CaseEntry{Key: "span.freeleech", Value: loader.Scalar{Value: "specific", Set: true}},
		),
	}
	v, found, err := New().Field(rows[0], block, nil)
	if err != nil || !found {
		t.Fatalf("case field err=%v found=%v", err, found)
	}
	if v != "star" {
		t.Fatalf("case arm = %q, want star (\"*\" defined first wins; deferring \"*\" would pick specific)", v)
	}
}

// TestContainsRemoveRespectsCase proves a `remove:` selector with :contains no
// longer strips case-mismatched elements Jackett keeps.
func TestContainsRemoveRespectsCase(t *testing.T) {
	t.Parallel()

	const page = `<div id="row"><div class="tags"><span>VIP</span><span>vip</span></div></div>`
	doc, err := New().ParseHTML([]byte(page))
	if err != nil {
		t.Fatal(err)
	}
	rows, err := doc.Rows(loader.RowsBlock{Selector: "div#row"})
	if err != nil || len(rows) != 1 {
		t.Fatalf("rows err=%v n=%d", err, len(rows))
	}

	block := loader.SelectorBlock{
		Selector: "div.tags",
		Remove:   `span:contains("VIP")`,
	}
	v, found, err := New().Field(rows[0], block, nil)
	if err != nil || !found {
		t.Fatalf("field err=%v found=%v", err, found)
	}
	if v != "vip" {
		t.Fatalf("text after remove = %q, want %q (case-insensitive :contains removed both spans)", v, "vip")
	}
}

// TestParseCSSEscapeHexBounds pins parseCSSEscape's hex-escape decoding,
// including the bound check on the uint64->rune conversion (CodeQL
// go/incorrect-integer-conversion): up to 6 hex digits (max 0xFFFFFF) can
// exceed utf8.MaxRune (0x10FFFF) or land on a surrogate half, both of which
// must degrade to the replacement character rather than trust the truncation.
func TestParseCSSEscapeHexBounds(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want string
	}{
		{"single ascii hex escape", `\41 `, "A"},           // U+0041 'A', trailing space consumed
		{"max valid code point", `\10FFFF `, "\U0010FFFF"}, // exactly utf8.MaxRune
		{"astral code point", `\1F600 `, "\U0001F600"},     // outside the BMP, still valid
		{"one past max valid", `\110000 `, "�"},            // first value to overflow utf8.MaxRune
		{"six f's, max parseable", `\FFFFFF `, "�"},        // 0xFFFFFF, far past utf8.MaxRune
		{"high surrogate half", `\D800 `, "�"},             // within utf8.MaxRune but not a valid rune
		{"low surrogate half", `\DFFF `, "�"},              // ditto
		{"nul escape", `\0 `, "\x00"},                      // 0 is in-range; not a surrogate
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, _, err := parseCSSEscape(tt.in, 0)
			if err != nil {
				t.Fatalf("parseCSSEscape(%q) err = %v", tt.in, err)
			}
			if got != tt.want {
				t.Fatalf("parseCSSEscape(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
