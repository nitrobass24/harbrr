package xspeeds

import (
	"context"
	"net/url"
	"slices"
	"testing"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
)

func TestBuildSearchTerm(t *testing.T) {
	tests := []struct {
		name  string
		query search.Query
		want  string
	}{
		{name: "empty"},
		{name: "dash and quote normalization", query: search.Query{Keywords: "A—–B `C´D‘E’F"}, want: "A B C D E F"},
		{name: "whitelist removal", query: search.Query{Keywords: "Movie: Name!? <x> &"}, want: "Movie Name x"},
		{name: "allowed punctuation and collapse", query: search.Query{Keywords: "C++ 100% [Group] / Path@Host"}, want: "C 100 [Group] / Path@Host"},
		{name: "year omitted", query: search.Query{Keywords: "Movie", Year: "2026"}, want: "Movie"},
		{name: "episode", query: search.Query{Keywords: "Show", Season: "1", Ep: "2"}, want: "Show S01E02"},
		{name: "season", query: search.Query{Keywords: "Show", Season: "3"}, want: "Show S03"},
		{name: "daily", query: search.Query{Keywords: "Show", Season: "2024", Ep: "01/15"}, want: "Show 2024 01 15"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := buildSearchTerm(test.query); got != test.want {
				t.Errorf("buildSearchTerm() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestSingleCategory(t *testing.T) {
	tests := []struct {
		name string
		in   []string
		want string
	}{
		{name: "none", want: "0"},
		{name: "one", in: []string{"70"}, want: "70"},
		{name: "duplicate", in: []string{"70", "70"}, want: "70"},
		{name: "multiple", in: []string{"70", "12"}, want: "0"},
		{name: "blank ignored", in: []string{"", "70", " "}, want: "70"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := singleCategory(test.in); got != test.want {
				t.Errorf("singleCategory(%v) = %q, want %q", test.in, got, test.want)
			}
		})
	}
}

func TestBrowseRequest(t *testing.T) {
	driver := newTestDriver(t, "https://xspeeds.example/root/", testConfig(), nil, nil)
	tests := []struct {
		name      string
		query     search.Query
		wantQuery url.Values
	}{
		{
			name: "empty browse",
			wantQuery: url.Values{
				"category": {"0"}, "include_dead_torrents": {"yes"}, "sort": {"added"}, "order": {"desc"},
			},
		},
		{
			name:  "encoded search",
			query: search.Query{Keywords: "A/B [Group]@Host", Categories: []string{"70"}, Offset: 50, Limit: 100},
			wantQuery: url.Values{
				"category": {"70"}, "include_dead_torrents": {"yes"}, "sort": {"added"}, "order": {"desc"},
				"do": {"search"}, "keywords": {"A/B [Group]@Host"}, "search_type": {"t_name"},
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request, err := driver.newBrowseRequest(t.Context(), test.query)
			if err != nil {
				t.Fatalf("newBrowseRequest: %v", err)
			}
			if request.Method != "GET" || request.URL.Path != "/root/browse.php" {
				t.Errorf("request = %s %s, want GET /root/browse.php", request.Method, request.URL.Path)
			}
			if request.Header.Get("Accept") != "text/html" {
				t.Errorf("Accept = %q, want text/html", request.Header.Get("Accept"))
			}
			if got := request.URL.Query(); !valuesEqual(got, test.wantQuery) {
				t.Errorf("query = %v, want %v", got, test.wantQuery)
			}
			if test.name == "encoded search" && request.URL.RawQuery == "" {
				t.Error("RawQuery is empty")
			}
		})
	}
}

func TestBrowseRequestCarriesCancellation(t *testing.T) {
	driver := newTestDriver(t, "https://xspeeds.example/", testConfig(), nil, nil)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	request, err := driver.newBrowseRequest(ctx, search.Query{})
	if err != nil {
		t.Fatalf("newBrowseRequest: %v", err)
	}
	if request.Context().Err() != context.Canceled {
		t.Errorf("request context error = %v, want context.Canceled", request.Context().Err())
	}
}

func valuesEqual(a, b url.Values) bool {
	if len(a) != len(b) {
		return false
	}
	for key, values := range a {
		if !slices.Equal(values, b[key]) {
			return false
		}
	}
	return true
}
