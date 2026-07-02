package selector

import (
	"testing"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/loader"
)

// TestRowsHTML checks row splitting and the "after" merge against Jackett.
func TestRowsHTML(t *testing.T) {
	t.Parallel()

	t.Run("one row per match", func(t *testing.T) {
		t.Parallel()
		doc, err := New().ParseHTML(readFixture(t, "rows.html"))
		if err != nil {
			t.Fatal(err)
		}
		rows, err := doc.Rows(loader.RowsBlock{Selector: "tr.row"})
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) != 2 {
			t.Fatalf("rows = %d, want 2", len(rows))
		}
		// Second row's name field.
		v, found, err := New().Field(rows[1], loader.SelectorBlock{Selector: "td.name"})
		if err != nil || !found {
			t.Fatalf("field err=%v found=%v", err, found)
		}
		if v != "Beta Release" {
			t.Fatalf("value=%q want Beta Release", v)
		}
	})

	t.Run("after merges following row children", func(t *testing.T) {
		t.Parallel()
		doc, err := New().ParseHTML(readFixture(t, "after.html"))
		if err != nil {
			t.Fatal(err)
		}
		after := 1
		rows, err := doc.Rows(loader.RowsBlock{Selector: "tr.r", After: &after})
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) != 2 {
			t.Fatalf("merged rows = %d, want 2", len(rows))
		}
		// After merge, the detail cell from the following row is now reachable
		// inside the kept row.
		v, found, err := New().Field(rows[0], loader.SelectorBlock{Selector: "td.detail"})
		if err != nil || !found {
			t.Fatalf("field err=%v found=%v", err, found)
		}
		if v != "First detail" {
			t.Fatalf("merged detail = %q, want First detail", v)
		}
		title, _, _ := New().Field(rows[0], loader.SelectorBlock{Selector: "td.title"})
		if title != "First" {
			t.Fatalf("title = %q, want First", title)
		}
	})

	t.Run("empty rows selector errors", func(t *testing.T) {
		t.Parallel()
		doc, _ := New().ParseHTML(readFixture(t, "rows.html"))
		if _, err := doc.Rows(loader.RowsBlock{}); err == nil {
			t.Fatal("expected error for empty rows selector")
		}
	})
}

// TestEdgeCasesHTML is the STANDING AngleSharp-vs-cascadia edge-case suite:
// :has / :contains scoping and whitespace/case behaviors that differ between
// the two engines until pinned here. New divergences must update this suite.
func TestEdgeCasesHTML(t *testing.T) {
	t.Parallel()
	row := func(t *testing.T) Row { return firstHTMLRow(t, "edge.html", "div#row") }

	tests := []struct {
		name      string
		block     loader.SelectorBlock
		wantValue string
		wantFound bool
	}{
		{
			name:      "has scoping selects parent with child",
			block:     loader.SelectorBlock{Selector: "div.status:has(i.ok)"},
			wantValue: "",
			wantFound: true,
		},
		{
			name:      "has scoping no match",
			block:     loader.SelectorBlock{Selector: "div.status:has(i.fail)"},
			wantFound: false,
		},
		{
			// Trim-only NormalizeSpace: the leading/trailing whitespace is removed
			// but the internal newline + space runs are preserved verbatim (matching
			// AngleSharp TextContent + ParseUtil.NormalizeSpace, which never
			// collapses). The fixture's <p.note> is "  multi\n     line    text  ".
			name:      "multiline note trimmed not collapsed",
			block:     loader.SelectorBlock{Selector: "p.note"},
			wantValue: "multi\n     line    text",
			wantFound: true,
		},
		{
			name:      "nested anchor first match only",
			block:     loader.SelectorBlock{Selector: "a.tag", Attribute: "href"},
			wantValue: "/t/1",
			wantFound: true,
		},
		{
			name: "case uses has scoping",
			block: loader.SelectorBlock{
				Selector: "div.tags",
				Case: map[string]loader.Scalar{
					"a.tag": {Value: "tagged", Set: true},
					"*":     {Value: "untagged", Set: true},
				},
			},
			wantValue: "tagged",
			wantFound: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, found, err := New().Field(row(t), tc.block)
			assertField(t, fieldResult{got, found, err}, tc.wantValue, tc.wantFound, false)
		})
	}
}

// TestSelfMatchHTML pins Jackett's "Dom.Matches(sel) ? Dom : QuerySelector(sel)"
// behavior: a field selector that matches the ROW element itself resolves to the
// row, not to a descendant (or not-found). cascadia's FindMatcher is
// descendant-only, so this requires the explicit self-match harbrr now adds.
func TestSelfMatchHTML(t *testing.T) {
	t.Parallel()
	row := firstHTMLRow(t, "edge.html", "div#row")

	// The row is <div id="row">; a selector matching it self-matches.
	v, found, err := New().Field(row, loader.SelectorBlock{Selector: "div#row", Attribute: "id"})
	if err != nil || !found {
		t.Fatalf("self-match err=%v found=%v", err, found)
	}
	if v != "row" {
		t.Fatalf("self-matched id = %q, want row", v)
	}

	// A non-self, non-descendant selector still misses.
	_, found, err = New().Field(row, loader.SelectorBlock{Selector: "div#absent"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if found {
		t.Fatal("non-matching selector should not be found")
	}
}

// TestRootSelectorHTML pins Jackett QuerySelector's :root handling: a ":root"
// prefix re-roots the query at the document element, so a row-scoped field can
// reach siblings/ancestors outside its own subtree. 119 vendored selectors use
// :root.
func TestRootSelectorHTML(t *testing.T) {
	t.Parallel()
	// Use rows.html; scope to the first row, then reach the OTHER row via :root.
	doc, err := New().ParseHTML(readFixture(t, "rows.html"))
	if err != nil {
		t.Fatal(err)
	}
	rows, err := doc.Rows(loader.RowsBlock{Selector: "tr.row"})
	if err != nil {
		t.Fatal(err)
	}

	// From row 0, ":root tr[data-id='200'] td.name" reaches the second row, which
	// a plain (descendant-scoped) selector could never see.
	block := loader.SelectorBlock{Selector: ":root tr[data-id='200'] td.name"}
	v, found, err := New().Field(rows[0], block)
	if err != nil || !found {
		t.Fatalf(":root cross-row err=%v found=%v", err, found)
	}
	if v != "Beta Release" {
		t.Fatalf(":root cross-row value = %q, want Beta Release", v)
	}
}
