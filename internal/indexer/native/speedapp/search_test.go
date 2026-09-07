package speedapp

import (
	"context"
	"errors"
	"fmt"
	stdhttp "net/http"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/normalizer"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
)

func TestBuildSearchURL(t *testing.T) {
	t.Parallel()
	d := testDriver(t, &scriptDoer{})
	tests := []struct {
		name        string
		query       search.Query
		page        int
		includePage bool
		perPage     int
		wantQuery   string
	}{
		{
			name:      "default browse",
			perPage:   100,
			wantQuery: "direction=desc&itemsPerPage=100&search=&sort=torrent.createdAt",
		},
		{
			name: "categories imdb season episode and page",
			query: search.Query{
				Keywords: "ignored", Categories: []string{"401", "402"}, IMDBID: "123", Season: "2", Ep: "3",
			},
			page: 3, includePage: true, perPage: 20,
			wantQuery: "categories%5B%5D=401&categories%5B%5D=402&direction=desc&episode=3&imdbId=tt0000123&itemsPerPage=20&page=3&season=2&sort=torrent.createdAt",
		},
		{
			name:      "invalid imdb falls back to keyword",
			query:     search.Query{Keywords: "Retro Movie", IMDBID: "TT123"},
			perPage:   100,
			wantQuery: "direction=desc&itemsPerPage=100&search=Retro+Movie&sort=torrent.createdAt",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := d.buildSearchURL(tt.query, tt.page, tt.includePage, tt.perPage)
			want := d.BaseURL + "api/torrent?" + tt.wantQuery
			if got != want {
				t.Errorf("URL = %q\nwant  %q", got, want)
			}
			for _, secret := range []string{testEmail, testPassword, testToken} {
				if strings.Contains(got, secret) {
					t.Errorf("URL leaked %q: %s", secret, got)
				}
			}
		})
	}
}

func TestItemsPerPageBoundsAndPageFormula(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		limit int
		want  int
	}{
		{limit: 0, want: 100},
		{limit: -1, want: 100},
		{limit: 20, want: 20},
		{limit: 100, want: 100},
		{limit: 101, want: 100},
	} {
		if got := itemsPerPage(tt.limit); got != tt.want {
			t.Errorf("itemsPerPage(%d) = %d, want %d", tt.limit, got, tt.want)
		}
	}

	d := testDriver(t, &scriptDoer{})
	got := d.buildSearchURL(search.Query{}, 40/20+1, true, itemsPerPage(20))
	if !strings.Contains(got, "page=3") {
		t.Errorf("offset/limit page formula missing page=3: %s", got)
	}
}

func TestFilteredPagingUsesPostCleanupOffsets(t *testing.T) {
	t.Parallel()
	pages := map[string]string{
		"": rowsJSON(
			t,
			testRow(1, "alpha beta one", ""),
			testRow(2, "alpha only", ""),
		),
		"2": rowsJSON(
			t,
			testRow(3, "other", "contains alpha and beta"),
			testRow(4, "noise", ""),
		),
		"3": rowsJSON(t, testRow(5, "alpha beta three", "")),
	}
	var mu sync.Mutex
	var requested []string
	doer := &scriptDoer{handler: func(req *stdhttp.Request, _ []byte) (*stdhttp.Response, error) {
		page := req.URL.Query().Get("page")
		mu.Lock()
		requested = append(requested, page)
		mu.Unlock()
		body, ok := pages[page]
		if !ok {
			return nil, fmt.Errorf("unexpected page %q", page)
		}
		return jsonResponse(stdhttp.StatusOK, body), nil
	}}
	d := testDriver(t, doer)
	seedDriverToken(d, testToken, 1)
	releases, err := d.Search(t.Context(), search.Query{Keywords: "alpha beta", Offset: 1, Limit: 2})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if got, want := releaseTitles(releases), []string{"other", "alpha beta three"}; !reflect.DeepEqual(got, want) {
		t.Errorf("titles = %v, want %v", got, want)
	}
	mu.Lock()
	gotPages := append([]string(nil), requested...)
	mu.Unlock()
	if want := []string{"", "2", "3"}; !reflect.DeepEqual(gotPages, want) {
		t.Errorf("pages = %v, want %v", gotPages, want)
	}
}

func TestFilteredPagingStopsOnShortPage(t *testing.T) {
	t.Parallel()
	doer := &scriptDoer{handler: func(*stdhttp.Request, []byte) (*stdhttp.Response, error) {
		return jsonResponse(stdhttp.StatusOK, rowsJSON(
			t,
			testRow(1, "alpha beta", ""),
			testRow(2, "noise", ""),
		)), nil
	}}
	d := testDriver(t, doer)
	seedDriverToken(d, testToken, 1)
	releases, err := d.Search(t.Context(), search.Query{Keywords: "alpha beta", Limit: 3})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if got := releaseTitles(releases); !reflect.DeepEqual(got, []string{"alpha beta"}) {
		t.Errorf("titles = %v", got)
	}
	if len(doer.records()) != 1 {
		t.Errorf("requests = %d, want 1", len(doer.records()))
	}
}

func TestFilteredPagingCancellation(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	doer := &scriptDoer{handler: func(*stdhttp.Request, []byte) (*stdhttp.Response, error) {
		cancel()
		return jsonResponse(stdhttp.StatusOK, rowsJSON(
			t,
			testRow(1, "noise one", ""),
			testRow(2, "noise two", ""),
		)), nil
	}}
	d := testDriver(t, doer)
	seedDriverToken(d, testToken, 1)
	_, err := d.Search(ctx, search.Query{Keywords: "alpha beta", Limit: 2})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if len(doer.records()) != 1 {
		t.Errorf("requests = %d, want no page after cancellation", len(doer.records()))
	}
}

func TestDirectPagingCompensatesUnalignedOffset(t *testing.T) {
	t.Parallel()
	pages := map[string]string{
		"1": rowsJSON(t, testRow(1, "one", ""), testRow(2, "two", "")),
		"2": rowsJSON(t, testRow(3, "three", ""), testRow(4, "four", "")),
	}
	doer := &scriptDoer{handler: func(req *stdhttp.Request, _ []byte) (*stdhttp.Response, error) {
		body, ok := pages[req.URL.Query().Get("page")]
		if !ok {
			return nil, fmt.Errorf("unexpected page %q", req.URL.Query().Get("page"))
		}
		return jsonResponse(stdhttp.StatusOK, body), nil
	}}
	d := testDriver(t, doer)
	seedDriverToken(d, testToken, 1)
	releases, err := d.Search(t.Context(), search.Query{Offset: 1, Limit: 2})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if got, want := releaseTitles(releases), []string{"two", "three"}; !reflect.DeepEqual(got, want) {
		t.Errorf("titles = %v, want %v", got, want)
	}
	if records := doer.records(); len(records) != 2 || !strings.Contains(records[0].rawQuery, "page=1") || !strings.Contains(records[1].rawQuery, "page=2") {
		t.Errorf("requests = %+v, want pages 1 and 2", records)
	}
}

func TestIDSearchSkipsCleanup(t *testing.T) {
	t.Parallel()
	doer := &scriptDoer{handler: func(req *stdhttp.Request, _ []byte) (*stdhttp.Response, error) {
		if req.URL.Query().Get("imdbId") != "tt0000123" || req.URL.Query().Has("search") {
			t.Errorf("query = %s, want imdb only", req.URL.RawQuery)
		}
		return jsonResponse(stdhttp.StatusOK, rowsJSON(t, testRow(1, "does not match", ""))), nil
	}}
	d := testDriver(t, doer)
	seedDriverToken(d, testToken, 1)
	releases, err := d.Search(t.Context(), search.Query{Keywords: "alpha beta", IMDBID: "123", Limit: 1})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(releases) != 1 {
		t.Errorf("ID search returned %d rows, want cleanup bypass", len(releases))
	}
}

func TestInvalidIMDBFallsBackToCleanup(t *testing.T) {
	t.Parallel()
	doer := &scriptDoer{handler: func(req *stdhttp.Request, _ []byte) (*stdhttp.Response, error) {
		if req.URL.Query().Get("search") != "alpha beta" || req.URL.Query().Has("imdbId") {
			t.Errorf("query = %s, want keyword only", req.URL.RawQuery)
		}
		return jsonResponse(stdhttp.StatusOK, rowsJSON(
			t,
			testRow(1, "Alpha Beta", ""),
			testRow(2, "does not match", ""),
		)), nil
	}}
	d := testDriver(t, doer)
	seedDriverToken(d, testToken, 1)
	releases, err := d.Search(t.Context(), search.Query{Keywords: "alpha beta", IMDBID: "TT123"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if got, want := releaseTitles(releases), []string{"Alpha Beta"}; !reflect.DeepEqual(got, want) {
		t.Errorf("titles = %v, want %v", got, want)
	}
}

func TestQueryCleanup(t *testing.T) {
	t.Parallel()
	releases := []*normalizer.Release{
		{Title: "Alpha only"},
		{Title: "ALPHA", Description: "contains Beta too"},
		{Title: "Déjà", Description: "vu"},
		{Title: "Noise"},
	}
	tests := []struct {
		query string
		want  []string
	}{
		{query: "alpha", want: []string{"Alpha only", "ALPHA"}},
		{query: "the alpha and beta of", want: []string{"ALPHA"}},
		{query: "déjà-vu", want: []string{"Déjà"}},
		{query: "a I the and", want: []string{"Alpha only", "ALPHA", "Déjà", "Noise"}},
	}
	for _, tt := range tests {
		if got := releaseTitles(cleanup(tt.query, releases)); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("cleanup(%q) = %v, want %v", tt.query, got, tt.want)
		}
	}
	if got, want := queryTerms("the Déjà_vu and βήτα"), []string{"Déjà_vu", "βήτα"}; !reflect.DeepEqual(got, want) {
		t.Errorf("queryTerms unicode/common = %v, want %v", got, want)
	}
}

func releaseTitles(releases []*normalizer.Release) []string {
	titles := make([]string, 0, len(releases))
	for _, release := range releases {
		titles = append(titles, release.Title)
	}
	return titles
}
