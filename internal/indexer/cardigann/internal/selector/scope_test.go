package selector

import (
	"testing"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/loader"
)

// scopePage mirrors torrentproject2's #similarfiles result rows: each row is a
// div of sibling spans (name/seeders/leechers/age/size). The SECOND span-list
// item deliberately buries a matching <span><a> inside a NESTED div so a faithful
// ":scope > span > a" (direct child of the row) can be told apart from a naive
// descendant search that would wrongly reach the nested anchor.
const scopePage = `<div id="similarfiles">
	<div>
		<span><a href="/t100">My Movie</a></span>
		<span>42</span>
		<span>7</span>
		<span>3 years ago</span>
		<span>1.4 GB</span>
		<div class="extra"><span><a href="/tNESTED">Nested Should Not Win</a></span></div>
	</div>
	<div>
		<span>plain text, no link</span>
		<span>1</span>
		<span>2</span>
		<span>2020-11-05 07:34:44</span>
		<span>900 MB</span>
		<div class="extra"><span><a href="/tNESTED2">Nested Only</a></span></div>
	</div>
</div>`

// TestScopeFieldExtraction drives the real Field path over torrentproject2's
// exact ":scope > …" selectors and checks each resolves to the correct
// direct-child element, reproducing AngleSharp's element-scoped QuerySelector.
func TestScopeFieldExtraction(t *testing.T) {
	t.Parallel()

	doc, err := New().ParseHTML([]byte(scopePage))
	if err != nil {
		t.Fatal(err)
	}
	rows, err := doc.Rows(loader.RowsBlock{Selector: `#similarfiles div:has(a[href^="/t"])`})
	if err != nil {
		t.Fatalf("Rows: %v", err)
	}
	// The rows selector matches every div that HAS a "/t" anchor descendant: both
	// outer rows plus the two nested wrapper divs = 4. The engine only extracts
	// from what the def iterates; here we assert against the two real rows.
	if len(rows) < 2 {
		t.Fatalf("rows = %d, want >= 2", len(rows))
	}

	tests := []struct {
		field    string
		selector string
		want     string
		found    bool
	}{
		// title: the direct-child span's anchor text, NOT the nested anchor.
		{"title", ":scope > span > a", "My Movie", true},
		{"seeders", ":scope > span:nth-child(2)", "42", true},
		{"leechers", ":scope > span:nth-child(3)", "7", true},
		{"date_ago", `:scope > span:nth-child(4):contains("ago")`, "3 years ago", true},
		{"size", ":scope > span:nth-child(5)", "1.4 GB", true},
	}
	for _, tc := range tests {
		t.Run(tc.field, func(t *testing.T) {
			t.Parallel()
			v, found, ferr := New().Field(rows[0], loader.SelectorBlock{Selector: tc.selector}, nil)
			if ferr != nil {
				t.Fatalf("Field(%q): %v", tc.selector, ferr)
			}
			if found != tc.found || v != tc.want {
				t.Fatalf("Field(%q) = (%q, %v), want (%q, %v)", tc.selector, v, found, tc.want, tc.found)
			}
		})
	}
}

// TestScopeDirectChildOnly is the anti-parity guard: a row whose only matching
// <span><a> is nested inside a child div must yield NO title for ":scope > span >
// a". A naive strip to a descendant "span > a" search would wrongly surface the
// nested anchor; the scope-relative walk must not.
func TestScopeDirectChildOnly(t *testing.T) {
	t.Parallel()

	doc, err := New().ParseHTML([]byte(scopePage))
	if err != nil {
		t.Fatal(err)
	}
	rows, err := doc.Rows(loader.RowsBlock{Selector: `#similarfiles > div`})
	if err != nil || len(rows) != 2 {
		t.Fatalf("rows err=%v n=%d, want 2", err, len(rows))
	}

	// rows[1] has no direct-child <span><a>; only the nested wrapper does.
	v, found, err := New().Field(rows[1], loader.SelectorBlock{Selector: ":scope > span > a"}, nil)
	if err != nil {
		t.Fatalf("Field: %v", err)
	}
	if found {
		t.Fatalf("title = %q found, want no direct-child match (nested anchor must not leak)", v)
	}

	// The date on that row is the 4th span with a ":" — proves :nth-child + a
	// filtered pseudo compile and match against direct children.
	date, found, err := New().Field(rows[1], loader.SelectorBlock{Selector: `:scope > span:nth-child(4):contains(":")`}, nil)
	if err != nil || !found || date != "2020-11-05 07:34:44" {
		t.Fatalf("date = (%q, %v, %v), want (2020-11-05 07:34:44, true, nil)", date, found, err)
	}
}

// TestScopeBare checks that a lone ":scope" resolves to the row element itself,
// matching AngleSharp (element.QuerySelectorAll(":scope") returns the element).
func TestScopeBare(t *testing.T) {
	t.Parallel()

	doc, err := New().ParseHTML([]byte(`<div id="row"><span>alpha</span></div>`))
	if err != nil {
		t.Fatal(err)
	}
	rows, err := doc.Rows(loader.RowsBlock{Selector: "div#row"})
	if err != nil || len(rows) != 1 {
		t.Fatalf("rows err=%v n=%d", err, len(rows))
	}
	v, found, err := New().Field(rows[0], loader.SelectorBlock{Selector: ":scope"}, nil)
	if err != nil || !found || v != "alpha" {
		t.Fatalf("bare :scope = (%q, %v, %v), want (alpha, true, nil)", v, found, err)
	}
}

// TestSplitSteps unit-tests the top-level combinator tokenizer, including the
// child vs descendant distinction and combinator bytes hidden inside :contains.
func TestSplitSteps(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want []selStep
	}{
		{
			name: "child chain",
			in:   ":scope > span > a",
			want: []selStep{{0, ":scope"}, {'>', "span"}, {'>', "a"}},
		},
		{
			name: "nth-child with contains keeps one compound",
			in:   `:scope > span:nth-child(4):contains("ago")`,
			want: []selStep{{0, ":scope"}, {'>', `span:nth-child(4):contains("ago")`}},
		},
		{
			name: "combinator inside contains is not a split point",
			in:   `:scope > a:contains("a > b")`,
			want: []selStep{{0, ":scope"}, {'>', `a:contains("a > b")`}},
		},
		{
			name: "descendant combinator",
			in:   ":scope span a",
			want: []selStep{{0, ":scope"}, {' ', "span"}, {' ', "a"}},
		},
		{
			name: "bare scope",
			in:   ":scope",
			want: []selStep{{0, ":scope"}},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := splitSteps(tc.in)
			if len(got) != len(tc.want) {
				t.Fatalf("splitSteps(%q) = %+v, want %+v", tc.in, got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("splitSteps(%q)[%d] = %+v, want %+v", tc.in, i, got[i], tc.want[i])
				}
			}
		})
	}
}

// TestHasScopePrefix guards the token boundary: ":scope" and its combinator forms
// are prefixes, but ":scoped"/":scope-foo" are not.
func TestHasScopePrefix(t *testing.T) {
	t.Parallel()

	yes := []string{":scope", ":scope > a", " :scope span", ":scope+div", ":scope~x"}
	no := []string{":scoped", ":scope-foo", "div :scope", "a.scope"}
	for _, s := range yes {
		if !hasScopePrefix(s) {
			t.Errorf("hasScopePrefix(%q) = false, want true", s)
		}
	}
	for _, s := range no {
		if hasScopePrefix(s) {
			t.Errorf("hasScopePrefix(%q) = true, want false", s)
		}
	}
}

// TestClassifyIncompatibleScopeGuard pins that the census self-guard excuses a
// :scope selector ONLY when matchScope can actually evaluate it. A supported
// combinator with a compilable compound is excused; an unsupported combinator
// (+ / ~, which matchScope rejects at runtime) or a non-compiling compound is
// NOT excused, so it surfaces as a genuine census failure instead of being
// green-lit into silent zero results.
func TestClassifyIncompatibleScopeGuard(t *testing.T) {
	t.Parallel()

	excused := []string{":scope > span > a", ":scope span", ":scope"}
	for _, s := range excused {
		if classifyIncompatible(s) == "" {
			t.Errorf("classifyIncompatible(%q) not excused, want a handled-:scope reason", s)
		}
	}

	notExcused := []string{":scope + span", ":scope ~ div"}
	for _, s := range notExcused {
		if r := classifyIncompatible(s); r != "" {
			t.Errorf("classifyIncompatible(%q) = %q, want \"\" (matchScope rejects this combinator)", s, r)
		}
	}
}
