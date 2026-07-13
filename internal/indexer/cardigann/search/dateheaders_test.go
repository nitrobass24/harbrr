package search

import (
	"testing"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/dateparse"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/loader"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/normalizer"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/selector"
)

// headerDeps wires the same date machinery the engine installs (dateparse ->
// registry), so the dateheaders backfill runs through the real filter + FromUnknown
// path a `date` field uses.
func headerDeps() Deps {
	p := dateparse.New()
	reg := NewFilterRegistry()
	reg.ParseDate = p.ParseDate
	reg.ParseRelTime = p.ParseRelTime
	return Deps{
		Selector:   selector.New(),
		Filters:    reg,
		Normalizer: normalizer.New(normalizer.WithBaseURL("https://t.invalid/")),
		Config:     map[string]string{},
		BaseURL:    "https://t.invalid/",
	}
}

// splitRows parses html and returns the rows the rowsSelector matches, in document
// order — the same rows ParseResults would iterate.
func splitRows(t *testing.T, html, rowsSelector string) []selector.Row {
	t.Helper()
	doc, err := selector.New().ParseHTML([]byte(html))
	if err != nil {
		t.Fatalf("ParseHTML: %v", err)
	}
	rows, err := doc.Rows(loader.RowsBlock{Selector: rowsSelector})
	if err != nil {
		t.Fatalf("Rows: %v", err)
	}
	return rows
}

func defWithDateHeaders(block *loader.SelectorBlock) *loader.Definition {
	def := &loader.Definition{}
	def.Search.Rows.DateHeaders = block
	return def
}

// boxingHeaders is the boxingtorrents shape: date-header rows and content rows are
// siblings in one tbody, and the header value goes through append/replace/dateparse
// exactly as the vendored def does.
const boxingHeaders = `<table class="t"><tbody>` +
	`<tr class="h"><td colspan="6"><b>Torrents added Monday, 1. Jan, 2024</b></td></tr>` +
	`<tr class="c"><td><a href="d.php?id=1">A</a></td></tr>` +
	`<tr class="c"><td><a href="d.php?id=2">B</a></td></tr>` +
	`<tr class="h"><td colspan="6"><b>Torrents added Tuesday, 2. Jan, 2024</b></td></tr>` +
	`<tr class="c"><td><a href="d.php?id=3">C</a></td></tr>` +
	`</tbody></table>`

func boxingBlock() *loader.SelectorBlock {
	return &loader.SelectorBlock{
		Selector: "td[colspan] > b",
		Filters: []loader.FilterBlock{
			{Name: "append", Args: loader.FilterArgs{" -07:00"}},
			{Name: "replace", Args: loader.FilterArgs{"Torrents added ", ""}},
			{Name: "dateparse", Args: loader.FilterArgs{"dddd, d. MMM, yyyy zzz"}},
		},
	}
}

// TestBackfillDateHeader_SiblingHeaders covers the boxingtorrents case: each
// content row adopts its nearest PRECEDING date-header sibling, walking past
// intervening content rows that do not match the dateheaders selector.
func TestBackfillDateHeader_SiblingHeaders(t *testing.T) {
	t.Parallel()
	deps := headerDeps()
	def := defWithDateHeaders(boxingBlock())
	rows := splitRows(t, boxingHeaders, `tr:has(a[href^="d.php?id="])`)
	if len(rows) != 3 {
		t.Fatalf("rows = %d, want 3", len(rows))
	}

	// id=1 sits directly after the Monday header; id=2 must walk past id=1 (a
	// content row that does not match the header selector) back to Monday; id=3
	// belongs to the Tuesday group.
	want := []string{
		"2024-01-01T00:00:00-07:00",
		"2024-01-01T00:00:00-07:00",
		"2024-01-02T00:00:00-07:00",
	}
	for i, row := range rows {
		rel := &normalizer.Release{}
		if err := backfillDateHeader(def, row, rel, Query{}, deps, ""); err != nil {
			t.Fatalf("row %d: backfillDateHeader: %v", i, err)
		}
		if rel.PublishDate != want[i] {
			t.Errorf("row %d PublishDate = %q, want %q", i, rel.PublishDate, want[i])
		}
	}
}

// TestBackfillDateHeader_ParentHop covers the hdgalaktik shape: the content rows
// are nested in a container, so the header is found only by hopping to the
// container's previous element sibling (the "?? ParentElement.PreviousElementSibling"
// leg of Jackett's walk). The header date comes from a querystring attribute.
func TestBackfillDateHeader_ParentHop(t *testing.T) {
	t.Parallel()
	const html = `<div class="wrap">` +
		`<div class="h"><a href="?date=2024-01-05">hdr</a></div>` +
		`<div class="group">` +
		`<div class="card"><a href="details?id=1">A</a></div>` +
		`<div class="card"><a href="details?id=2">B</a></div>` +
		`</div></div>`
	block := &loader.SelectorBlock{
		Selector:  `a[href*="date="]`,
		Attribute: "href",
		Filters: []loader.FilterBlock{
			{Name: "querystring", Args: loader.FilterArgs{"date"}},
			{Name: "dateparse", Args: loader.FilterArgs{"yyyy-MM-dd"}},
		},
	}
	deps := headerDeps()
	def := defWithDateHeaders(block)
	rows := splitRows(t, html, ".card")
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(rows))
	}
	for i, row := range rows {
		rel := &normalizer.Release{}
		if err := backfillDateHeader(def, row, rel, Query{}, deps, ""); err != nil {
			t.Fatalf("row %d: backfillDateHeader: %v", i, err)
		}
		if got, want := rel.PublishDate, "2024-01-05T00:00:00Z"; got != want {
			t.Errorf("row %d PublishDate = %q, want %q", i, got, want)
		}
	}
}

// TestBackfillDateHeader_Edges covers the branches around a missing/pre-set date.
func TestBackfillDateHeader_Edges(t *testing.T) {
	t.Parallel()
	// A lone content row with no preceding header anywhere.
	const orphan = `<table><tbody><tr class="c"><td><a href="d.php?id=1">A</a></td></tr></tbody></table>`

	optional := true
	optionalBlock := boxingBlock()
	optionalBlock.Optional = &optional

	tests := []struct {
		name       string
		block      *loader.SelectorBlock
		html       string
		respType   string
		preset     string
		wantErr    bool
		wantPublic string
	}{
		{
			// Not optional + no header found: Jackett throws "No date header row
			// found", which drops the row. backfill returns an error so the HTML
			// row-skip drops it identically.
			name:    "no header, not optional -> error (row dropped)",
			block:   boxingBlock(),
			html:    orphan,
			wantErr: true,
		},
		{
			name:       "no header, optional -> kept with empty date",
			block:      optionalBlock,
			html:       orphan,
			wantPublic: "",
		},
		{
			// A present PublishDate is never overwritten (Jackett backfills only
			// PublishDate == DateTime.MinValue).
			name:       "present date is not overwritten",
			block:      boxingBlock(),
			html:       boxingHeaders,
			preset:     "2020-05-05T00:00:00Z",
			wantPublic: "2020-05-05T00:00:00Z",
		},
		{
			// dateheaders is HTML-only; a JSON parse must not attempt the walk.
			name:       "json response type -> no-op",
			block:      boxingBlock(),
			html:       boxingHeaders,
			respType:   responseTypeJSON,
			wantPublic: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			deps := headerDeps()
			def := defWithDateHeaders(tt.block)
			rows := splitRows(t, tt.html, `tr:has(a[href^="d.php?id="]), .card`)
			if len(rows) == 0 {
				t.Fatalf("no rows matched")
			}
			rel := &normalizer.Release{PublishDate: tt.preset}
			err := backfillDateHeader(def, rows[0], rel, Query{}, deps, tt.respType)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil (PublishDate=%q)", rel.PublishDate)
				}
				return
			}
			if err != nil {
				t.Fatalf("backfillDateHeader: %v", err)
			}
			if rel.PublishDate != tt.wantPublic {
				t.Errorf("PublishDate = %q, want %q", rel.PublishDate, tt.wantPublic)
			}
		})
	}
}
