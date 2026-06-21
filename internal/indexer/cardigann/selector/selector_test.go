package selector

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/loader"
)

// scalar is a test helper building a present loader.Scalar.
func scalar(v string) *loader.Scalar { return &loader.Scalar{Value: v, Set: true} }

// boolPtr is a test helper for optional bool flags.
func boolPtr(b bool) *bool { return &b }

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("reading fixture %s: %v", name, err)
	}
	return data
}

// firstRow parses an HTML fixture and returns the first row for rows.selector.
func firstHTMLRow(t *testing.T, fixture, rowsSel string) Row {
	t.Helper()
	doc, err := New().ParseHTML(readFixture(t, fixture))
	if err != nil {
		t.Fatalf("ParseHTML: %v", err)
	}
	rows, err := doc.Rows(loader.RowsBlock{Selector: rowsSel})
	if err != nil {
		t.Fatalf("Rows: %v", err)
	}
	if len(rows) == 0 {
		t.Fatalf("no rows matched %q", rowsSel)
	}
	return rows[0]
}

// TestFieldHTML is part of the STANDING selector fixture suite: an ongoing
// compatibility check of cascadia/goquery extraction against Jackett's
// AngleSharp handleSelector, NOT a one-time verification. Every row asserts the
// exact extracted value Jackett would produce on the same bytes.
func TestFieldHTML(t *testing.T) {
	t.Parallel()
	row := func(t *testing.T) Row { return firstHTMLRow(t, "rows.html", "tr.row") }

	tests := []struct {
		name      string
		block     loader.SelectorBlock
		wantValue string
		wantFound bool
		wantErr   bool
	}{
		{
			// Jackett's NormalizeSpace TRIMS only; it does NOT collapse internal
			// whitespace runs (that is the separate, uncalled NormalizeMultiSpaces).
			// AngleSharp's TextContent preserves the source whitespace verbatim, so
			// the internal triple-spaces survive and only the ends are trimmed.
			name:      "innertext trimmed not collapsed",
			block:     loader.SelectorBlock{Selector: "td.name"},
			wantValue: "Alpha   Release   2024",
			wantFound: true,
		},
		{
			name:      "attribute extraction",
			block:     loader.SelectorBlock{Selector: "a.dl", Attribute: "href"},
			wantValue: "/dl/100",
			wantFound: true,
		},
		{
			name:      "row-level attribute",
			block:     loader.SelectorBlock{Attribute: "data-id"},
			wantValue: "100",
			wantFound: true,
		},
		{
			name:      "missing attribute optional returns not found",
			block:     loader.SelectorBlock{Selector: "a.dl", Attribute: "data-nope"},
			wantFound: false,
		},
		{
			name:      "missing selector returns not found",
			block:     loader.SelectorBlock{Selector: "td.does-not-exist"},
			wantFound: false,
		},
		{
			name:      "text literal fallback ignores dom",
			block:     loader.SelectorBlock{Selector: "td.name", Text: scalar("literal")},
			wantValue: "literal",
			wantFound: true,
		},
		{
			name: "remove strips subelement before text",
			// td.links contains two anchors; remove the magnet, read the rest.
			block:     loader.SelectorBlock{Selector: "td.links", Remove: "a.magnet"},
			wantValue: "grab",
			wantFound: true,
		},
		{
			name: "case switch first match wins",
			block: loader.SelectorBlock{
				Selector: "td.flag",
				Case: map[string]loader.Scalar{
					"span.freeleech": {Value: "yes", Set: true},
					"*":              {Value: "no", Set: true},
				},
			},
			wantValue: "yes",
			wantFound: true,
		},
		{
			name: "case switch catch-all star",
			block: loader.SelectorBlock{
				Selector: "td.size",
				Case: map[string]loader.Scalar{
					"span.freeleech": {Value: "yes", Set: true},
					"*":              {Value: "no", Set: true},
				},
			},
			wantValue: "no",
			wantFound: true,
		},
		{
			name: "case switch no match not found",
			block: loader.SelectorBlock{
				Selector: "td.size",
				Case: map[string]loader.Scalar{
					"span.freeleech": {Value: "yes", Set: true},
				},
			},
			wantFound: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, found, err := New().Field(row(t), tc.block)
			assertField(t, fieldResult{got, found, err}, tc.wantValue, tc.wantFound, tc.wantErr)
		})
	}
}

// TestFieldDefersRequiredDecision pins the scope boundary with the engine: this
// stage does NOT itself apply Selector.Optional / Default or throw on a required
// empty match. Jackett's handleSelector takes a `required` flag and throws when a
// required selector matches nothing, but harbrr splits that decision out — Field
// reports found=false on every empty extraction (no error), and the engine field
// loop inspects Optional/Default and wraps ErrSelectorNoMatch when the
// value is required. This test guarantees the engine cannot silently inherit a
// swallowed error: an empty match is an explicit found=false, never a fabricated
// value and never an error here.
func TestFieldDefersRequiredDecision(t *testing.T) {
	t.Parallel()

	row := firstHTMLRow(t, "rows.html", "tr.row")
	cases := []loader.SelectorBlock{
		{Selector: "td.does-not-exist"},              // missing selector
		{Selector: "a.dl", Attribute: "data-absent"}, // missing attribute
		{Selector: "td.flag", Case: map[string]loader.Scalar{ // no case arm matches
			"span.nope": {Value: "x", Set: true},
		}},
	}
	for i := range cases {
		v, found, err := New().Field(row, cases[i])
		if err != nil {
			t.Fatalf("case %d: unexpected error %v", i, err)
		}
		if found {
			t.Fatalf("case %d: expected found=false, got value=%q", i, v)
		}
		if v != "" {
			t.Fatalf("case %d: expected empty value, got %q", i, v)
		}
	}
}

type fieldResult struct {
	value string
	found bool
	err   error
}

func assertField(t *testing.T, got fieldResult, wantValue string, wantFound, wantErr bool) {
	t.Helper()
	if wantErr {
		if got.err == nil {
			t.Fatalf("expected error, got value=%q found=%v", got.value, got.found)
		}
		return
	}
	if got.err != nil {
		t.Fatalf("unexpected error: %v", got.err)
	}
	if got.found != wantFound {
		t.Fatalf("found = %v, want %v (value=%q)", got.found, wantFound, got.value)
	}
	if got.found && got.value != wantValue {
		t.Fatalf("value = %q, want %q", got.value, wantValue)
	}
}
