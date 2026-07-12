package nzbindex

import (
	stdhttp "net/http"
	"strings"
	"testing"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
)

func TestBuildSearchURL(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		apikey string
		query  search.Query
		has    []string
		hasNot []string
	}{
		{
			name:   "basic search, no apikey",
			query:  search.Query{Keywords: "test"},
			has:    []string{testBaseURL + "/api/search?", "max=100", "q=test"},
			hasNot: []string{"key=", "p="},
		},
		{
			name:   "apikey sent as key param",
			apikey: testAPIKey,
			query:  search.Query{Keywords: "test"},
			has:    []string{"key=" + testAPIKey, "q=test"},
		},
		{
			name:  "explicit limit becomes max",
			query: search.Query{Keywords: "x", Limit: 25},
			has:   []string{"max=25"},
		},
		{
			name:   "empty term omits q",
			query:  search.Query{},
			has:    []string{"max=100"},
			hasNot: []string{"q="},
		},
		{
			name:  "aligned offset becomes page",
			query: search.Query{Keywords: "x", Limit: 100, Offset: 200},
			has:   []string{"p=2"},
		},
		{
			name:   "first page omits p",
			query:  search.Query{Keywords: "x", Limit: 100, Offset: 0},
			hasNot: []string{"p="},
		},
		{
			name:  "tv season/ep folded into q",
			query: search.Query{Keywords: "show", Season: "1", Ep: "2", Mode: "tv-search"},
			has:   []string{"S01E02"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			d := testDriver(t, map[string]string{"apikey": tt.apikey}, nil)
			got := d.buildSearchURL(tt.query)
			for _, h := range tt.has {
				if !strings.Contains(got, h) {
					t.Errorf("buildSearchURL = %q, want substring %q", got, h)
				}
			}
			for _, h := range tt.hasNot {
				if strings.Contains(got, h) {
					t.Errorf("buildSearchURL = %q, must NOT contain %q", got, h)
				}
			}
		})
	}
}

// TestSearchSendsJSONAccept proves the search issues a GET with Accept: application/json.
func TestSearchSendsJSONAccept(t *testing.T) {
	t.Parallel()
	doer := &scriptDoer{handler: func(*stdhttp.Request) *stdhttp.Response { return jsonResponse(string(readGolden(t, "search.json"))) }}
	d := testDriver(t, nil, doer)
	if _, err := d.Search(t.Context(), search.Query{Keywords: "test"}); err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(doer.reqs) != 1 {
		t.Fatalf("issued %d requests, want 1", len(doer.reqs))
	}
	if doer.reqs[0].method != "GET" || doer.reqs[0].accept != "application/json" {
		t.Errorf("request = %s Accept:%q, want GET application/json", doer.reqs[0].method, doer.reqs[0].accept)
	}
}
