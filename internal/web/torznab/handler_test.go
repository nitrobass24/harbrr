package torznab

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/loader"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/mapper"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/normalizer"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
)

const testAPIKey = "harbrr-test-key" //nolint:gosec // G101: synthetic test API key, not a real credential

// fakeIndexer is a Provider-backed Indexer for the handler tests: it serves
// canned capabilities + releases and records the search query it received.
type fakeIndexer struct {
	info          IndexerInfo
	caps          *mapper.Capabilities
	releases      []*normalizer.Release
	searchErr     error
	gotQuery      search.Query
	needsResolver bool
	resolvePrefix string // when set, ResolveDownload returns resolvePrefix+link
	resolveErr    error
}

func (f *fakeIndexer) Info() IndexerInfo                  { return f.info }
func (f *fakeIndexer) Capabilities() *mapper.Capabilities { return f.caps }

func (f *fakeIndexer) Search(q search.Query) ([]*normalizer.Release, error) {
	f.gotQuery = q
	return f.releases, f.searchErr
}

func (f *fakeIndexer) NeedsResolver() bool { return f.needsResolver }

func (f *fakeIndexer) ResolveDownload(link string) (string, error) {
	if f.resolveErr != nil {
		return "", f.resolveErr
	}
	if f.resolvePrefix != "" {
		return f.resolvePrefix + link, nil
	}
	return link, nil
}

type fakeProvider map[string]Indexer

func (p fakeProvider) Indexer(id string) (Indexer, bool) { i, ok := p[id]; return i, ok }

// testCaps builds capabilities for a demo indexer: categorymappings 1->Movies
// (+custom 100001 "HD Movies"), 2->TV; modes search, tv-search [q,season,tvdbid]
// (no imdbid), movie-search [q,imdbid].
func testCaps(t *testing.T) *mapper.Capabilities {
	t.Helper()
	def := &loader.Definition{
		ID:    "demo",
		Links: []string{"https://demo.test/"},
		Caps: loader.Caps{
			CategoryMappings: []loader.CategoryMapping{
				{ID: loader.Scalar{Value: "1", Set: true}, Cat: "Movies", Desc: "HD Movies"},
				{ID: loader.Scalar{Value: "2", Set: true}, Cat: "TV"},
			},
			// tv-search WITHOUT tvdbid/imdbid (like 1337x/eztv/limetorrents — the
			// majority of real trackers); movie-search WITH imdbid but no tmdbid.
			Modes: loader.Modes{
				Search:      []string{"q"},
				TVSearch:    []string{"q", "season", "ep"},
				MovieSearch: []string{"q", "imdbid"},
			},
		},
	}
	caps, err := mapper.Build(def)
	if err != nil {
		t.Fatalf("mapper.Build: %v", err)
	}
	return caps
}

func demoRelease(title, link string, cats []int) *normalizer.Release {
	return &normalizer.Release{
		Title: title, Link: link, Size: 1024, Categories: cats,
		Seeders: 1, Peers: 1, PublishDate: "2024-01-02T03:04:05Z",
		DownloadVolumeFactor: 1, UploadVolumeFactor: 1,
	}
}

func newTestHandler(t *testing.T, idx *fakeIndexer) http.Handler {
	t.Helper()
	return NewHandler(
		fakeProvider{"demo": idx},
		WithAPIKey(testAPIKey),
		WithClock(func() time.Time { return time.Date(2026, time.June, 13, 12, 0, 0, 0, time.UTC) }),
	)
}

func demoIndexer(t *testing.T) *fakeIndexer {
	return &fakeIndexer{
		info: IndexerInfo{ID: "demo", Name: "Demo Tracker", Description: "demo", SiteLink: "https://demo.test/", Type: "public"},
		caps: testCaps(t),
		releases: []*normalizer.Release{
			demoRelease("Movie A", "https://demo.test/dl/1.torrent", []int{2000}),
		},
	}
}

// richIndexer advertises every search mode with rich params, for the
// successful-typed-mode-search tests.
func richIndexer(t *testing.T) *fakeIndexer {
	t.Helper()
	def := &loader.Definition{
		ID: "rich", Links: []string{"https://rich.test/"},
		Caps: loader.Caps{
			CategoryMappings: []loader.CategoryMapping{
				{ID: loader.Scalar{Value: "1", Set: true}, Cat: "Movies"},
				{ID: loader.Scalar{Value: "2", Set: true}, Cat: "TV"},
				{ID: loader.Scalar{Value: "3", Set: true}, Cat: "Audio"},
				{ID: loader.Scalar{Value: "4", Set: true}, Cat: "Books"},
			},
			Modes: loader.Modes{
				Search:      []string{"q"},
				TVSearch:    []string{"q", "season", "ep"},
				MovieSearch: []string{"q", "imdbid"},
				MusicSearch: []string{"q", "album", "artist", "label", "track"},
				BookSearch:  []string{"q", "title", "author"},
			},
		},
	}
	caps, err := mapper.Build(def)
	if err != nil {
		t.Fatalf("mapper.Build: %v", err)
	}
	return &fakeIndexer{
		info:     IndexerInfo{ID: "rich", Name: "Rich", Description: "rich", SiteLink: "https://rich.test/", Type: "public"},
		caps:     caps,
		releases: []*normalizer.Release{demoRelease("Result", "https://rich.test/dl/1.torrent", []int{2000})},
	}
}

// richDo drives a request against the rich indexer at /indexers/rich/.
func richDo(t *testing.T, idx *fakeIndexer, rawQuery string) *httptest.ResponseRecorder {
	t.Helper()
	h := NewHandler(fakeProvider{"rich": idx}, WithAPIKey(testAPIKey),
		WithClock(func() time.Time { return time.Date(2026, time.June, 13, 12, 0, 0, 0, time.UTC) }))
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet,
		"/api/v2.0/indexers/rich/results/torznab?"+rawQuery+"&apikey="+testAPIKey, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// TestHandlerTypedModeSearches drives a successful search in each typed mode to a
// 200 feed and asserts the mode-specific params thread into the engine query.
func TestHandlerTypedModeSearches(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		query  string
		verify func(*testing.T, search.Query)
	}{
		{"tvsearch", "t=tvsearch&q=show&season=1&ep=2", func(t *testing.T, q search.Query) {
			if q.Season != "1" || q.Ep != "2" {
				t.Errorf("season/ep = %q/%q, want 1/2", q.Season, q.Ep)
			}
		}},
		{"movie", "t=movie&q=film&year=2020", func(t *testing.T, q search.Query) {
			if q.Year != "2020" {
				t.Errorf("year = %q, want 2020", q.Year)
			}
		}},
		{"music", "t=music&album=A&artist=B&label=L&track=T", func(t *testing.T, q search.Query) {
			if q.Album != "A" || q.Artist != "B" || q.Label != "L" || q.Track != "T" {
				t.Errorf("music params = %q/%q/%q/%q, want A/B/L/T", q.Album, q.Artist, q.Label, q.Track)
			}
		}},
		{"book", "t=book&title=Ti&author=Au", func(t *testing.T, q search.Query) {
			if q.BookTitle != "Ti" || q.Author != "Au" {
				t.Errorf("book params = %q/%q, want Ti/Au", q.BookTitle, q.Author)
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			idx := richIndexer(t)
			rec := richDo(t, idx, tt.query)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200; body:\n%s", rec.Code, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), "<item>") {
				t.Errorf("%s feed has no items:\n%s", tt.name, rec.Body.String())
			}
			tt.verify(t, idx.gotQuery)
		})
	}
}

// TestHandlerMalformedParams confirms garbage scalar params degrade cleanly to a
// valid 200 feed (no panic): bad/zero/over-max limit, negative offset, and a cat
// list with non-numeric entries.
func TestHandlerMalformedParams(t *testing.T) {
	t.Parallel()
	queries := []string{
		"t=search&q=x&limit=abc",
		"t=search&q=x&limit=0",
		"t=search&q=x&limit=100000",
		"t=search&q=x&offset=-5",
		"t=search&q=x&offset=abc",
		"t=search&q=x&cat=foo,bar",
		"t=search&q=x&cat=2000,foo",
	}
	for _, q := range queries {
		t.Run(q, func(t *testing.T) {
			t.Parallel()
			rec := do(t, newTestHandler(t, demoIndexer(t)), q)
			if rec.Code != http.StatusOK {
				t.Errorf("status = %d, want 200 (clean degradation); body:\n%s", rec.Code, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), "<channel>") {
				t.Errorf("malformed-param request did not produce a valid feed:\n%s", rec.Body.String())
			}
		})
	}
}

// TestHandlerUnmappedCatPassesThrough pins the documented Phase-3 behavior: when
// every requested cat maps to no tracker category, the query categories are empty
// and the engine's full result set is returned (Jackett returns empty; this is a
// [Tracked: Phase 4] divergence).
// TestHandlerUnmappedCatFiltersResults: a requested cat that maps to no tracker
// category drives the search with no tracker categories (the demo def declares no
// default:true cats), and the response-side category filter (Jackett
// FilterResults) drops releases whose categories don't intersect the requested
// cat. The demo release is category 2000, so cat=9999 yields an empty feed.
func TestHandlerUnmappedCatFiltersResults(t *testing.T) {
	t.Parallel()
	idx := demoIndexer(t)
	rec := do(t, newTestHandler(t, idx), "t=search&cat=9999")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if len(idx.gotQuery.Categories) != 0 {
		t.Errorf("unmapped cat should yield empty tracker categories, got %v", idx.gotQuery.Categories)
	}
	if strings.Contains(rec.Body.String(), "<item>") {
		t.Errorf("unmapped cat should filter out the category-2000 release:\n%s", rec.Body.String())
	}
}

// TestHandlerResolvesDownloadLinks: when the indexer needs a resolver, each
// served release's link is rewritten via ResolveDownload before serialization.
func TestHandlerResolvesDownloadLinks(t *testing.T) {
	t.Parallel()
	idx := demoIndexer(t)
	idx.needsResolver = true
	idx.resolvePrefix = "https://harbrr.proxy/dl?u="
	rec := do(t, newTestHandler(t, idx), "t=search&q=movie")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "https://harbrr.proxy/dl?u=") {
		t.Errorf("resolver-needing indexer should rewrite served links:\n%s", rec.Body.String())
	}
}

// TestHandlerDirectLinkNotResolved: a direct-link tracker (NeedsResolver=false,
// the Phase 5 case) serves its link unchanged — ResolveDownload is never called.
func TestHandlerDirectLinkNotResolved(t *testing.T) {
	t.Parallel()
	idx := demoIndexer(t) // needsResolver defaults false
	idx.resolvePrefix = "https://should-not-apply/"
	rec := do(t, newTestHandler(t, idx), "t=search&q=movie")
	if strings.Contains(rec.Body.String(), "should-not-apply") {
		t.Errorf("direct-link tracker must not resolve links:\n%s", rec.Body.String())
	}
}

// do issues a GET to the torznab endpoint with the given raw query (apikey is
// appended unless the query already sets one).
func do(t *testing.T, h http.Handler, rawQuery string) *httptest.ResponseRecorder {
	t.Helper()
	if !strings.Contains(rawQuery, "apikey=") && !strings.Contains(rawQuery, "noauth") {
		rawQuery += "&apikey=" + testAPIKey
	}
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v2.0/indexers/demo/results/torznab?"+rawQuery, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestHandlerCaps(t *testing.T) {
	t.Parallel()
	rec := do(t, newTestHandler(t, demoIndexer(t)), "t=caps")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != contentTypeFeed {
		t.Errorf("content-type = %q, want %q", ct, contentTypeFeed)
	}
	body := rec.Body.String()
	for _, want := range []string{"<caps>", `<tv-search available="yes"`, `<category id="2000"`, `<category id="100001"`} {
		if !strings.Contains(body, want) {
			t.Errorf("caps missing %q in:\n%s", want, body)
		}
	}
}

func TestHandlerSearchResults(t *testing.T) {
	t.Parallel()
	idx := demoIndexer(t)
	rec := do(t, newTestHandler(t, idx), "t=search&q=movie")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != contentTypeFeed {
		t.Errorf("content-type = %q, want %q", ct, contentTypeFeed)
	}
	if idx.gotQuery.Keywords != "movie" {
		t.Errorf("query keywords = %q, want movie", idx.gotQuery.Keywords)
	}
	if !strings.Contains(rec.Body.String(), "<title>Movie A</title>") {
		t.Errorf("results missing the release:\n%s", rec.Body.String())
	}
}

func TestHandlerAuth(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		query string
	}{
		{"missing apikey", "t=caps&noauth=1"},
		{"wrong apikey", "t=caps&apikey=nope"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			rec := do(t, newTestHandler(t, demoIndexer(t)), tt.query)
			// Jackett returns HTTP 200 + <error> for credential failures so *arr
			// parses the error code rather than treating it as a transport failure.
			if rec.Code != http.StatusOK {
				t.Errorf("status = %d, want 200", rec.Code)
			}
			if !strings.Contains(rec.Body.String(), `<error code="100"`) {
				t.Errorf("want error 100, got:\n%s", rec.Body.String())
			}
		})
	}
}

func TestHandlerUnknownIndexer(t *testing.T) {
	t.Parallel()
	h := newTestHandler(t, demoIndexer(t))
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v2.0/indexers/ghost/results/torznab?t=caps&apikey="+testAPIKey, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `<error code="201"`) {
		t.Errorf("want error 201, got:\n%s", rec.Body.String())
	}
}

// TestHandlerErrorGating covers the requests harbrr REJECTS: an unknown t, an
// unadvertised mode, and an id param the gated mode does not support.
func TestHandlerErrorGating(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		query    string
		wantCode int // HTTP
		wantErr  string
	}{
		{"unknown t", "t=bogus", http.StatusBadRequest, `code="202"`},
		{"mode not advertised", "t=music&q=x", http.StatusBadRequest, `code="203"`},
		// movie-search advertises imdbid but not tmdbid -> tmdbid rejected.
		{"tmdbid unsupported by movie", "t=movie&tmdbid=1396", http.StatusBadRequest, `code="203"`},
		// tv-search does not advertise imdbid and AllowTVSearchIMDB is off -> rejected.
		{"imdbid unsupported by tv", "t=tvsearch&imdbid=tt0000001", http.StatusBadRequest, `code="203"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			rec := do(t, newTestHandler(t, demoIndexer(t)), tt.query)
			if rec.Code != tt.wantCode {
				t.Errorf("status = %d, want %d", rec.Code, tt.wantCode)
			}
			if !strings.Contains(rec.Body.String(), tt.wantErr) {
				t.Errorf("want %q in:\n%s", tt.wantErr, rec.Body.String())
			}
		})
	}
}

// TestHandlerIDSearchAccepted covers the id searches harbrr must ACCEPT, matching
// Jackett's gate (which only rejects imdbid/tmdbid for movie/tv). The key case is
// tvdbid on tv-search: real trackers advertise tv-search WITHOUT listing tvdbid,
// and Jackett still accepts the (most common) Sonarr tvdbid query.
func TestHandlerIDSearchAccepted(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		query     string
		wantField func(search.Query) string
		want      string
	}{
		{"tvdbid on tv-search (not param-gated)", "t=tvsearch&tvdbid=81189", func(q search.Query) string { return q.TVDBID }, "81189"},
		{"imdbid on movie (advertised)", "t=movie&imdbid=tt0903747", func(q search.Query) string { return q.IMDBID }, "tt0903747"},
		{"imdbid on general search (never gated)", "t=search&imdbid=tt0903747", func(q search.Query) string { return q.IMDBID }, "tt0903747"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			idx := demoIndexer(t)
			rec := do(t, newTestHandler(t, idx), tt.query)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200; body:\n%s", rec.Code, rec.Body.String())
			}
			if got := tt.wantField(idx.gotQuery); got != tt.want {
				t.Errorf("query field = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestHandlerCategoryMapping(t *testing.T) {
	t.Parallel()
	idx := demoIndexer(t)
	// cat=2000 (Movies) maps to tracker category "1"; cat=100001 (custom) also "1".
	do(t, newTestHandler(t, idx), "t=search&cat=2000")
	if got := idx.gotQuery.Categories; len(got) != 1 || got[0] != "1" {
		t.Errorf("cat=2000 -> tracker categories %v, want [1]", got)
	}
}

func TestHandlerNoResultsEmptyFeed(t *testing.T) {
	t.Parallel()
	idx := demoIndexer(t)
	idx.releases = nil
	rec := do(t, newTestHandler(t, idx), "t=search&q=nothing")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "<channel>") || strings.Contains(body, "<item>") {
		t.Errorf("no-results feed should be a channel with zero items:\n%s", body)
	}
}

func TestHandlerInternalErrorIsRedacted(t *testing.T) {
	t.Parallel()
	idx := demoIndexer(t)
	idx.searchErr = errors.New("cardigann: search failed for tracker with passkey=topsecret12345")
	rec := do(t, newTestHandler(t, idx), "t=search&q=x")
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `<error code="900"`) {
		t.Errorf("want error 900, got:\n%s", body)
	}
	// The served body must never echo the raw error (which could embed a secret).
	if strings.Contains(body, "topsecret12345") || strings.Contains(body, "passkey") {
		t.Errorf("error body leaked the underlying error:\n%s", body)
	}
}

func TestHandlerDedupAndPaging(t *testing.T) {
	t.Parallel()
	idx := demoIndexer(t)
	idx.releases = []*normalizer.Release{
		demoRelease("A", "https://demo.test/dl/1.torrent", []int{2000}),
		demoRelease("A dup", "https://demo.test/dl/1.torrent", []int{2000}), // same guid (link) -> de-duped
		demoRelease("B", "https://demo.test/dl/2.torrent", []int{2000}),
		demoRelease("C", "https://demo.test/dl/3.torrent", []int{2000}),
	}
	t.Run("dedup by guid", func(t *testing.T) {
		rec := do(t, newTestHandler(t, idx), "t=search&q=x")
		if n := strings.Count(rec.Body.String(), "<item>"); n != 3 {
			t.Errorf("item count = %d, want 3 (one duplicate guid collapsed)", n)
		}
	})
	t.Run("limit and offset", func(t *testing.T) {
		rec := do(t, newTestHandler(t, idx), "t=search&q=x&offset=1&limit=1")
		body := rec.Body.String()
		if n := strings.Count(body, "<item>"); n != 1 {
			t.Errorf("item count = %d, want 1 (limit=1)", n)
		}
		if !strings.Contains(body, "<title>B</title>") {
			t.Errorf("offset=1 should start at B:\n%s", body)
		}
	})
	t.Run("offset past end is empty", func(t *testing.T) {
		rec := do(t, newTestHandler(t, idx), "t=search&q=x&offset=99")
		if n := strings.Count(rec.Body.String(), "<item>"); n != 0 {
			t.Errorf("item count = %d, want 0 (offset past end)", n)
		}
	})
}

func TestHandlerSelfURLHasNoAPIKey(t *testing.T) {
	t.Parallel()
	rec := do(t, newTestHandler(t, demoIndexer(t)), "t=search&q=x")
	body := rec.Body.String()
	if strings.Contains(body, testAPIKey) {
		t.Errorf("atom:link self URL leaked the apikey:\n%s", body)
	}
	if !strings.Contains(body, `<atom:link href="http://example.com/api/v2.0/indexers/demo/results/torznab"`) {
		t.Errorf("self URL not built from the request path without query:\n%s", body)
	}
}
