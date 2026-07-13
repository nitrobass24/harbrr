package search

import (
	"testing"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/internal/selector"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/loader"
)

// TestRenderRowsSelector covers evaluating a templated row selector before it is
// compiled — the HD-Space case, where the raw `{{ if .Config.freeleech }}…{{ end }}`
// would otherwise be handed to the CSS compiler and fail.
func TestRenderRowsSelector(t *testing.T) {
	t.Parallel()
	const hdspace = `table.lista[width="100%"] > tbody > style ~ tr{{ if .Config.freeleech }}:has(img[src="gold/gold.png"]){{ else }}{{ end }}, ` +
		`table.lista[width="100%"] > tbody > style ~ tr{{ if .Config.freeleech }}:has(img[src="images/sf.png"]){{ else }}{{ end }}`

	tests := []struct {
		name     string
		selector string
		config   map[string]string
		want     string
	}{
		{
			"hdspace freeleech off collapses to plain tr",
			hdspace,
			map[string]string{},
			`table.lista[width="100%"] > tbody > style ~ tr, table.lista[width="100%"] > tbody > style ~ tr`,
		},
		{
			"hdspace freeleech on keeps the :has guard",
			hdspace,
			map[string]string{"freeleech": "true"},
			`table.lista[width="100%"] > tbody > style ~ tr:has(img[src="gold/gold.png"]), ` +
				`table.lista[width="100%"] > tbody > style ~ tr:has(img[src="images/sf.png"])`,
		},
		{
			"plain selector is returned unchanged (no template eval)",
			"table > tbody > tr.row", nil,
			"table > tbody > tr.row",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			deps := Deps{Config: tt.config, BaseURL: "https://t.invalid/"}
			got, err := renderRowsSelector(loader.RowsBlock{Selector: tt.selector}, Query{}, deps)
			if err != nil {
				t.Fatalf("renderRowsSelector: %v", err)
			}
			if got.Selector != tt.want {
				t.Errorf("selector =\n  %q\nwant\n  %q", got.Selector, tt.want)
			}
		})
	}
}

// TestRenderRowsSelector_CompilesAndSplits is the end-to-end gate: a templated row
// selector, once rendered, actually compiles and splits a real document into rows
// (the raw template previously failed compilation with "bytes left over").
func TestRenderRowsSelector_CompilesAndSplits(t *testing.T) {
	t.Parallel()
	const html = `<table class="lista" width="100%"><tbody><style></style>` +
		`<tr><td>row one</td></tr><tr><td>row two</td></tr></tbody></table>`
	blk, err := renderRowsSelector(
		loader.RowsBlock{Selector: `table.lista[width="100%"] > tbody > style ~ tr{{ if .Config.freeleech }}:has(img){{ end }}`},
		Query{}, Deps{Config: map[string]string{}, BaseURL: "https://t.invalid/"},
	)
	if err != nil {
		t.Fatalf("renderRowsSelector: %v", err)
	}
	doc, err := selector.New().ParseHTML([]byte(html))
	if err != nil {
		t.Fatalf("ParseHTML: %v", err)
	}
	rows, err := doc.Rows(blk)
	if err != nil {
		t.Fatalf("rendered row selector failed to compile/split: %v", err)
	}
	if len(rows) != 2 {
		t.Errorf("rows = %d, want 2", len(rows))
	}
}
