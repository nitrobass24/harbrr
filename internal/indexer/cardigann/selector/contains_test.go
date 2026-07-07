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

// TestContainsCaseBlockArm reproduces the wihd.yml category hazard: two
// multi-:contains case arms where the arm ordered first is a case-mismatched
// near-miss. With cascadia's case-insensitive :contains the first arm matched
// anyway and returned the wrong category id; case-sensitive matching falls
// through to the arm whose literal matches the page text exactly.
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

	// orderedCases sorts arms, putting the "Bluray Remux" arm before the
	// "Bluray remux 4K" arm ('R' < 'r'), mirroring wihd's def order where the
	// plain-Remux arm precedes the 4K arm.
	block := loader.SelectorBlock{
		Selector: "div#row",
		Case: map[string]loader.Scalar{
			`a:contains("Films"):contains("Bluray Remux")`:    {Value: "hd-remux", Set: true},
			`a:contains("Films"):contains("Bluray remux 4K")`: {Value: "uhd-remux", Set: true},
		},
	}
	v, found, err := New().Field(rows[0], block)
	if err != nil || !found {
		t.Fatalf("case field err=%v found=%v", err, found)
	}
	if v != "uhd-remux" {
		t.Fatalf("case arm = %q, want uhd-remux (case-insensitive :contains would pick hd-remux)", v)
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
	v, found, err := New().Field(rows[0], block)
	if err != nil || !found {
		t.Fatalf("field err=%v found=%v", err, found)
	}
	if v != "vip" {
		t.Fatalf("text after remove = %q, want %q (case-insensitive :contains removed both spans)", v, "vip")
	}
}
