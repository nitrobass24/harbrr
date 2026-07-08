package search

import (
	"errors"
	"strings"
	"testing"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/filter"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/loader"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/normalizer"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/selector"
)

// searchErrorDef parses a private-tracker def whose search block declares a
// Search.Error selector (jpopsuki/gigatorrents shape) alongside real rows/fields.
// The error selector matches the tracker's error page; a normal results page has
// no such element and parses through to a release.
const searchErrorDef = `---
id: errblk
name: Error Block Fixture
description: search.error evaluation
type: private
language: en-US
encoding: UTF-8
links:
  - https://err.invalid/
caps:
  categorymappings:
    - {id: 1, cat: Movies}
  modes:
    search: [q]
search:
  path: /browse
  inputs:
    q: "{{ .Keywords }}"
  error:
    - selector: div.errorpage
  rows:
    selector: table > tbody > tr.result
  fields:
    category:
      text: Movies
    title:
      selector: a.title
    download:
      selector: a.title
      attribute: href
    seeders:
      selector: span.seeders
    size:
      selector: span.size
`

// searchErrorMessageDef is the helltorrents shape: the error block carries a
// Message selector block (here a literal text override) so the thrown message is
// the block's text, not the matched element's inner text.
const searchErrorMessageDef = `---
id: errmsg
name: Error Message Fixture
description: search.error with message selector
type: private
language: en-US
encoding: UTF-8
links:
  - https://err.invalid/
caps:
  categorymappings:
    - {id: 1, cat: Movies}
  modes:
    search: [q]
search:
  path: /browse
  error:
    - selector: img[src="denied.png"]
      message:
        text: "you do not have permission to download!"
  rows:
    selector: table > tbody > tr.result
  fields:
    category:
      text: Movies
    title:
      selector: a.title
    download:
      selector: a.title
      attribute: href
    seeders:
      selector: span.seeders
    size:
      selector: span.size
`

const (
	// errorPage200 is a tracker error served with HTTP 200 while logged in: the
	// error selector matches and there are no result rows. Jackett throws "Error:
	// Database error."; harbrr must return ErrTrackerError, not a silent empty page.
	errorPage200 = `<html><body><div class="errorpage">Database error.</div></body></html>`
	// deniedPage200 trips the message-selector error block.
	deniedPage200 = `<html><body><img src="denied.png"></body></html>`
	// resultsPage is a normal results page: the error selector finds nothing, so a
	// single release is parsed.
	resultsPage = `<html><body><table><tbody>` +
		`<tr class="result"><td><a class="title" href="/dl/1">Ubuntu 24.04</a>` +
		`<span class="seeders">5</span><span class="size">1 GB</span></td></tr>` +
		`</tbody></table></body></html>`
)

func searchErrorDeps() Deps {
	return Deps{
		Filters:    filter.NewRegistry(),
		Normalizer: normalizer.New(normalizer.WithBaseURL("https://err.invalid/")),
		BaseURL:    "https://err.invalid/",
	}
}

// TestParseResults_SearchError is the parity gate for U5-F4: a Search.Error
// selector matching the HTML response is a loud, message-bearing ErrTrackerError
// (Jackett's checkForError throw), NOT a silent empty slice; a normal page with
// the same error block defined but not matching parses its rows unchanged.
func TestParseResults_SearchError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		def         string
		body        string
		wantErr     bool
		wantMessage string // substring expected in the error
		wantRows    int
	}{
		{
			name:        "text-extracted error message",
			def:         searchErrorDef,
			body:        errorPage200,
			wantErr:     true,
			wantMessage: "Error: Database error.",
		},
		{
			name:        "message selector overrides matched text",
			def:         searchErrorMessageDef,
			body:        deniedPage200,
			wantErr:     true,
			wantMessage: "Error: you do not have permission to download!",
		},
		{
			name:     "no error match parses rows",
			def:      searchErrorDef,
			body:     resultsPage,
			wantErr:  false,
			wantRows: 1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			def, err := loader.Parse([]byte(tt.def))
			if err != nil {
				t.Fatalf("loader.Parse: %v", err)
			}
			rels, err := ParseResults(def, []byte(tt.body), "", Query{Keywords: "ubuntu"}, searchErrorDeps())
			if !tt.wantErr {
				if err != nil {
					t.Fatalf("ParseResults: %v, want normal parse", err)
				}
				if len(rels) != tt.wantRows {
					t.Errorf("releases = %d, want %d", len(rels), tt.wantRows)
				}
				return
			}
			if !errors.Is(err, ErrTrackerError) {
				t.Fatalf("error = %v, want ErrTrackerError", err)
			}
			// A tracker refusal must NOT be misclassified as a parse failure.
			if errors.Is(err, ErrParseError) {
				t.Errorf("tracker error wrongly wrapped as ErrParseError: %v", err)
			}
			if !strings.Contains(err.Error(), tt.wantMessage) {
				t.Errorf("error = %q, want it to contain %q", err.Error(), tt.wantMessage)
			}
			if rels != nil {
				t.Errorf("releases = %v, want nil on error", rels)
			}
		})
	}
}

// TestCheckSearchError_BranchExclusion pins the branch scope: the error check runs
// only on the HTML branch (respType "") — Jackett's JSON and XML branches never
// call checkForError — and is a no-op when the def declares no error blocks.
func TestCheckSearchError_BranchExclusion(t *testing.T) {
	t.Parallel()

	def, err := loader.Parse([]byte(searchErrorDef))
	if err != nil {
		t.Fatalf("loader.Parse: %v", err)
	}
	eng := selector.New()
	doc, err := eng.ParseHTML([]byte(errorPage200))
	if err != nil {
		t.Fatalf("ParseHTML: %v", err)
	}

	// HTML branch: the error selector matches, so this must error.
	if err := checkSearchError(def, doc, "", eng); !errors.Is(err, ErrTrackerError) {
		t.Errorf("html branch: err = %v, want ErrTrackerError", err)
	}
	// JSON and XML branches skip the check even though the same doc would match.
	for _, rt := range []string{responseTypeJSON, responseTypeXML} {
		if err := checkSearchError(def, doc, rt, eng); err != nil {
			t.Errorf("%s branch: err = %v, want nil (check skipped)", rt, err)
		}
	}
	// No error blocks declared: no-op even on the HTML branch.
	noErr, err := loader.Parse([]byte(strings.Replace(searchErrorDef,
		"  error:\n    - selector: div.errorpage\n", "", 1)))
	if err != nil {
		t.Fatalf("loader.Parse (no error block): %v", err)
	}
	if len(noErr.Search.Error) != 0 {
		t.Fatalf("test setup: expected the error block to be stripped")
	}
	if err := checkSearchError(noErr, doc, "", eng); err != nil {
		t.Errorf("no error block: err = %v, want nil", err)
	}
}

// TestExecute_SearchErrorNotParseError proves the classification choice end-to-end:
// an HTTP 200 error page surfaces through Execute as ErrTrackerError and is NOT
// re-wrapped as ErrParseError (which would fire a spurious parse_error health
// event). Jackett throws loudly here with no health category.
func TestExecute_SearchErrorNotParseError(t *testing.T) {
	t.Parallel()

	def, err := loader.Parse([]byte(searchErrorDef))
	if err != nil {
		t.Fatalf("loader.Parse: %v", err)
	}
	doer := &redirectDoer{t: t, steps: []redirectStep{
		{wantMethod: "GET", wantURL: "https://err.invalid/browse?q=ubuntu", body: errorPage200},
	}}
	_, err = Execute(t.Context(), def, Query{Keywords: "ubuntu"}, nil, doer, searchErrorDeps())
	if !errors.Is(err, ErrTrackerError) {
		t.Fatalf("Execute error = %v, want ErrTrackerError", err)
	}
	if errors.Is(err, ErrParseError) {
		t.Errorf("tracker error misclassified as ErrParseError: %v", err)
	}
	if !strings.Contains(err.Error(), "Error: Database error.") {
		t.Errorf("error = %q, want the tracker message", err.Error())
	}
}
