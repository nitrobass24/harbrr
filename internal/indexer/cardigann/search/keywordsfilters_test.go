package search

import (
	"net/url"
	"testing"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/loader"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/normalizer"
)

// TestBuildRequests_KeywordsFilters proves search.keywordsfilters transform the
// .Keywords request variable — in both the path template and the inputs — while
// .Query.Keywords stays RAW, reproducing Jackett's
// variables[".Keywords"] = ApplyFilters(variables[".Query.Keywords"], Search.Keywordsfilters).
func TestBuildRequests_KeywordsFilters(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		filters  []loader.FilterBlock
		query    Query
		wantQ    string
		wantRawQ string
		wantPath string
	}{
		{
			name:     "no filters is a no-op",
			filters:  nil,
			query:    Query{Keywords: "big buck bunny"},
			wantQ:    "big buck bunny",
			wantRawQ: "big buck bunny",
			wantPath: "/search/big buck bunny",
		},
		{
			name:     "re_replace joins words",
			filters:  []loader.FilterBlock{{Name: "re_replace", Args: loader.FilterArgs{`\s+`, "+"}}},
			query:    Query{Keywords: "big buck bunny"},
			wantQ:    "big+buck+bunny",
			wantRawQ: "big buck bunny",
			wantPath: "/search/big+buck+bunny",
		},
		{
			name: "filter chain applies in order",
			filters: []loader.FilterBlock{
				{Name: "tolower"},
				{Name: "re_replace", Args: loader.FilterArgs{`\s+`, "."}},
			},
			query:    Query{Keywords: "Big Buck Bunny"},
			wantQ:    "big.buck.bunny",
			wantRawQ: "Big Buck Bunny",
			wantPath: "/search/big.buck.bunny",
		},
		{
			name:     "episode token joins before filters run",
			filters:  []loader.FilterBlock{{Name: "re_replace", Args: loader.FilterArgs{`\s+`, "."}}},
			query:    Query{Keywords: "Big Buck", Season: "1", Ep: "2"},
			wantQ:    "Big.Buck.S01E02",
			wantRawQ: "Big Buck S01E02",
			wantPath: "/search/Big.Buck.S01E02",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			def := &loader.Definition{
				Links: []string{"https://kw.invalid/"},
				Search: loader.Search{
					Path:            "/search/{{ .Keywords }}",
					KeywordsFilters: tt.filters,
					Inputs: loader.NewInputsBlock(
						loader.InputEntry{Key: "q", Value: loader.Scalar{Value: "{{ .Keywords }}", Set: true}},
						loader.InputEntry{Key: "rawq", Value: loader.Scalar{Value: "{{ .Query.Keywords }}", Set: true}},
					),
				},
			}
			deps := Deps{BaseURL: "https://kw.invalid/", Filters: NewFilterRegistry()}

			reqs, err := buildRequests(def, tt.query, deps)
			if err != nil {
				t.Fatalf("buildRequests: %v", err)
			}
			if len(reqs) != 1 {
				t.Fatalf("reqs = %d, want 1", len(reqs))
			}
			u, err := url.Parse(reqs[0].url)
			if err != nil {
				t.Fatalf("parsing built URL: %v", err)
			}
			if got := u.Path; got != tt.wantPath {
				t.Errorf("path = %q, want %q (.Keywords in path template)", got, tt.wantPath)
			}
			if got := u.Query().Get("q"); got != tt.wantQ {
				t.Errorf("input q = %q, want %q (.Keywords)", got, tt.wantQ)
			}
			if got := u.Query().Get("rawq"); got != tt.wantRawQ {
				t.Errorf("input rawq = %q, want %q (.Query.Keywords is the raw joined term, unfiltered)", got, tt.wantRawQ)
			}
		})
	}
}

// keywordsFiltersDef is a schema-valid definition whose keywordsfilters strip
// the word "bunny" from the keyword term before andmatch runs.
const keywordsFiltersDef = `---
id: kwfilters
name: Keywords Filters Fixture
description: keywordsfilters feed andmatch
language: en-US
type: public
encoding: UTF-8
links:
  - https://kw.invalid/
caps:
  categorymappings:
    - {id: 1, cat: Movies}
  modes:
    search: [q]
search:
  path: /browse
  inputs:
    q: "{{ .Keywords }}"
  keywordsfilters:
    - name: re_replace
      args: ["(?i)\\bbunny\\b", ""]
  rows:
    selector: div.row
    filters:
      - name: andmatch
  fields:
    category:
      selector: div.row
      attribute: data-cat
    title:
      selector: a.title
    download:
      selector: a.title
      attribute: href
    size:
      selector: span.size
    seeders:
      selector: span.seeders
`

// TestParseResults_KeywordsFiltersAndMatch proves the andmatch row filter reads
// the keywordsfilters-FILTERED keyword term (Jackett's andmatch reads the
// .Keywords variable set after Keywordsfilters ran): with "bunny" stripped by
// the filter, a title lacking "bunny" survives andmatch that would otherwise
// drop it on the raw term.
func TestParseResults_KeywordsFiltersAndMatch(t *testing.T) {
	t.Parallel()

	def, err := loader.Parse([]byte(keywordsFiltersDef))
	if err != nil {
		t.Fatalf("loader.Parse: %v", err)
	}
	body := []byte(`<html><body>
<div class="row" data-cat="1"><a class="title" href="/dl/1">Big Buck Movie 1080p</a><span class="size">1 GB</span><span class="seeders">5</span></div>
<div class="row" data-cat="1"><a class="title" href="/dl/2">Sintel 1080p</a><span class="size">2 GB</span><span class="seeders">3</span></div>
</body></html>`)
	deps := Deps{
		Filters:    NewFilterRegistry(),
		Normalizer: normalizer.New(normalizer.WithBaseURL("https://kw.invalid/")),
		BaseURL:    "https://kw.invalid/",
	}

	releases, err := ParseResults(def, body, "", Query{Keywords: "big buck bunny"}, deps)
	if err != nil {
		t.Fatalf("ParseResults: %v", err)
	}
	if len(releases) != 1 {
		t.Fatalf("releases = %d, want 1 (andmatch on filtered 'big buck', not raw 'big buck bunny')", len(releases))
	}
	if releases[0].Title != "Big Buck Movie 1080p" {
		t.Errorf("title = %q, want %q", releases[0].Title, "Big Buck Movie 1080p")
	}
}
