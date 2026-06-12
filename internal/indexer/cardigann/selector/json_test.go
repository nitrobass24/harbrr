package selector

import (
	"testing"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/loader"
)

func firstJSONRow(t *testing.T, fixture, rowsSel string) Row {
	t.Helper()
	doc, err := New().ParseJSON(readFixture(t, fixture))
	if err != nil {
		t.Fatalf("ParseJSON: %v", err)
	}
	rows, err := doc.Rows(loader.RowsBlock{Selector: rowsSel})
	if err != nil {
		t.Fatalf("Rows: %v", err)
	}
	if len(rows) == 0 {
		t.Fatalf("no rows for %q", rowsSel)
	}
	return rows[0]
}

// mustParseJSON parses a fixture and fails fast on a parse error, so the subtests
// assert on Rows behavior without a parse failure silently masquerading as one.
func mustParseJSON(t *testing.T, fixture string) *Document {
	t.Helper()
	doc, err := New().ParseJSON(readFixture(t, fixture))
	if err != nil {
		t.Fatalf("ParseJSON %q: %v", fixture, err)
	}
	return doc
}

// TestRowsJSON checks array resolution for both a nested path and the "$" root.
func TestRowsJSON(t *testing.T) {
	t.Parallel()

	t.Run("nested data path", func(t *testing.T) {
		t.Parallel()
		doc := mustParseJSON(t, "rows.json")
		rows, err := doc.Rows(loader.RowsBlock{Selector: "data"})
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) != 2 {
			t.Fatalf("rows = %d, want 2", len(rows))
		}
	})

	t.Run("root array dollar", func(t *testing.T) {
		t.Parallel()
		doc := mustParseJSON(t, "rootarray.json")
		rows, err := doc.Rows(loader.RowsBlock{Selector: "$"})
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) != 3 {
			t.Fatalf("rows = %d, want 3", len(rows))
		}
		v, _, _ := New().Field(rows[2], loader.SelectorBlock{Selector: "name"})
		if v != "three" {
			t.Fatalf("name = %q, want three", v)
		}
	})

	t.Run("missing array with flag yields no rows", func(t *testing.T) {
		t.Parallel()
		doc := mustParseJSON(t, "rows.json")
		rows, err := doc.Rows(loader.RowsBlock{
			Selector:                        "nope",
			MissingAttributeEqualsNoResults: boolPtr(true),
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(rows) != 0 {
			t.Fatalf("rows = %d, want 0", len(rows))
		}
	})

	t.Run("missing array without flag errors", func(t *testing.T) {
		t.Parallel()
		doc := mustParseJSON(t, "rows.json")
		if _, err := doc.Rows(loader.RowsBlock{Selector: "nope"}); err == nil {
			t.Fatal("expected error for missing rows array")
		}
	})
}

// TestFieldJSON is the STANDING JSON extraction suite: dotted paths, array
// indices, leaf coercion, and the case switch with "*", asserted against
// JToken.ToString() / String.Join semantics.
func TestFieldJSON(t *testing.T) {
	t.Parallel()
	row := func(t *testing.T) Row { return firstJSONRow(t, "rows.json", "data") }

	tests := []struct {
		name      string
		block     loader.SelectorBlock
		wantValue string
		wantFound bool
	}{
		{
			name:      "string leaf",
			block:     loader.SelectorBlock{Selector: "title"},
			wantValue: "Alpha Release 2024",
			wantFound: true,
		},
		{
			name:      "integer leaf canonical",
			block:     loader.SelectorBlock{Selector: "id"},
			wantValue: "100",
			wantFound: true,
		},
		{
			name:      "large integer not float-formatted",
			block:     loader.SelectorBlock{Selector: "size"},
			wantValue: "1610612736",
			wantFound: true,
		},
		{
			name:      "float leaf canonical",
			block:     loader.SelectorBlock{Selector: "ratio"},
			wantValue: "1.5",
			wantFound: true,
		},
		{
			name:      "bool true canonical",
			block:     loader.SelectorBlock{Selector: "freeleech"},
			wantValue: "True",
			wantFound: true,
		},
		{
			name:      "array joined with comma",
			block:     loader.SelectorBlock{Selector: "tags"},
			wantValue: "x264,1080p",
			wantFound: true,
		},
		{
			name:      "nested dotted path",
			block:     loader.SelectorBlock{Selector: "meta.year"},
			wantValue: "2024",
			wantFound: true,
		},
		{
			name:      "leading dot trimmed",
			block:     loader.SelectorBlock{Selector: ".meta.uploader"},
			wantValue: "alice",
			wantFound: true,
		},
		{
			name:      "missing path not found",
			block:     loader.SelectorBlock{Selector: "missing"},
			wantFound: false,
		},
		{
			name: "case equality match",
			block: loader.SelectorBlock{
				Selector: "category",
				Case: map[string]loader.Scalar{
					"movies": {Value: "2000", Set: true},
					"tv":     {Value: "5000", Set: true},
				},
			},
			wantValue: "2000",
			wantFound: true,
		},
		{
			name: "case star catch-all",
			block: loader.SelectorBlock{
				Selector: "category",
				Case: map[string]loader.Scalar{
					"music": {Value: "3000", Set: true},
					"*":     {Value: "8000", Set: true},
				},
			},
			wantValue: "8000",
			wantFound: true,
		},
		{
			name:      "text fallback",
			block:     loader.SelectorBlock{Text: scalar("magnet:?xt=fixed")},
			wantValue: "magnet:?xt=fixed",
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

// TestArrayIndexPath checks numeric path segments index into arrays, in both the
// dotted form ("tags.0") and Newtonsoft bracket form ("files[0].name") that the
// corpus actually uses — 73 vendored JSON defs select via "files[0].name".
func TestArrayIndexPath(t *testing.T) {
	t.Parallel()
	row := func(t *testing.T) Row { return firstJSONRow(t, "rows.json", "data") }

	tests := []struct {
		name      string
		selector  string
		wantValue string
		wantFound bool
	}{
		{"dotted index", "tags.0", "x264", true},
		{"dotted index out of range", "tags.9", "", false},
		{"bracket index then key", "files[0].name", "alpha.mkv", true},
		{"bracket index second element", "files[1].name", "alpha.nfo", true},
		{"bracket index out of range", "files[5].name", "", false},
		{"bracket index on missing key", "nope[0].name", "", false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			v, found, err := New().Field(row(t), loader.SelectorBlock{Selector: tc.selector})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if found != tc.wantFound {
				t.Fatalf("found = %v, want %v (value=%q)", found, tc.wantFound, v)
			}
			if found && v != tc.wantValue {
				t.Fatalf("%s = %q, want %q", tc.selector, v, tc.wantValue)
			}
		})
	}
}

// TestRootArrayBracketIndex checks the "$[0].id" corpus form: a bracket subscript
// directly on the root array. SelectToken("$[0].id") in Newtonsoft, which a
// vendored def uses on its rows selector's parent.
func TestRootArrayBracketIndex(t *testing.T) {
	t.Parallel()

	doc, err := New().ParseJSON(readFixture(t, "rootarray.json"))
	if err != nil {
		t.Fatalf("ParseJSON: %v", err)
	}
	// Treat the whole root array as a single row to query "$[N]" forms against.
	rows, err := doc.Rows(loader.RowsBlock{Selector: "$"})
	if err != nil {
		t.Fatalf("Rows: %v", err)
	}
	// Each element is itself a row; resolve a bracket-indexed path against the
	// resolver directly to mirror SelectToken("$[2].name").
	got, ok := resolvePath([]any{
		map[string]any{"name": "one"},
		map[string]any{"name": "two"},
		map[string]any{"name": "three"},
	}, "$[2].name")
	if !ok || got != "three" {
		t.Fatalf("$[2].name = %v ok=%v, want three", got, ok)
	}
	if len(rows) != 3 {
		t.Fatalf("rows = %d, want 3", len(rows))
	}
}

// TestMalformedJSONErrors pins that ParseJSON fails loud on invalid JSON rather
// than silently yielding an empty document.
func TestMalformedJSONErrors(t *testing.T) {
	t.Parallel()
	if _, err := New().ParseJSON([]byte("{not valid json")); err == nil {
		t.Fatal("expected error parsing malformed JSON")
	}
}

// TestEvalTemplateSeam verifies the injected EvalTemplate is applied to selector
// strings, case values, and text exactly where Jackett interleaves it, and that
// the default is identity.
func TestEvalTemplateSeam(t *testing.T) {
	t.Parallel()

	// Eval rewrites the magic selector token, brackets literal case/text values,
	// and passes plain path selectors through unchanged so they still resolve.
	e := &Engine{EvalTemplate: func(s string) (string, error) {
		switch s {
		case "{{ .sel }}":
			return "title", nil
		case "cat", "lit":
			return "[" + s + "]", nil
		default:
			return s, nil
		}
	}}
	row := firstJSONRow(t, "rows.json", "data")

	// Selector string is template-evaluated to "title".
	v, _, err := e.Field(row, loader.SelectorBlock{Selector: "{{ .sel }}"})
	if err != nil {
		t.Fatal(err)
	}
	if v != "Alpha Release 2024" {
		t.Fatalf("templated selector value = %q", v)
	}

	// Case value is template-evaluated.
	v, _, err = e.Field(row, loader.SelectorBlock{
		Selector: "category",
		Case:     map[string]loader.Scalar{"*": {Value: "cat", Set: true}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if v != "[cat]" {
		t.Fatalf("templated case value = %q, want [cat]", v)
	}

	// Text is template-evaluated.
	v, _, err = e.Field(row, loader.SelectorBlock{Text: scalar("lit")})
	if err != nil {
		t.Fatal(err)
	}
	if v != "[lit]" {
		t.Fatalf("templated text = %q, want [lit]", v)
	}
}
