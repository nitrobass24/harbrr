package nebulance

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	stdhttp "net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/login"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
	"github.com/autobrr/harbrr/internal/indexer/native"
)

const testAPIKey = "NBL-SYNTHETIC-API-KEY"

type recordedRequest struct {
	method string
	url    *url.URL
	accept string
}

type scriptDoer struct {
	handler func(*stdhttp.Request) (*stdhttp.Response, error)
	reqs    []recordedRequest
}

func (d *scriptDoer) Do(req *stdhttp.Request) (*stdhttp.Response, error) {
	d.reqs = append(d.reqs, recordedRequest{method: req.Method, url: req.URL, accept: req.Header.Get("Accept")})
	return d.handler(req)
}

func response(status int, body string) *stdhttp.Response {
	return &stdhttp.Response{
		StatusCode: status,
		Header:     stdhttp.Header{},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func liveDriver(t *testing.T, doer interface {
	Do(*stdhttp.Request) (*stdhttp.Response, error)
},
) *driver {
	t.Helper()
	family := Families()[0]
	built, err := New(native.Params{
		Def:   family.Definition,
		Cfg:   map[string]string{"apikey": testAPIKey},
		Doer:  doer,
		Clock: func() time.Time { return time.Unix(1_700_000_000, 0).UTC() },
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return built.(*driver)
}

func TestBuildSearchURL(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		query   search.Query
		want    map[string]string
		wantNot []string
		ok      bool
	}{
		{
			name: "browse defaults", query: search.Query{}, ok: true,
			want:    map[string]string{"action": "search", "age": ">0", "per_page": "100"},
			wantNot: []string{"page", "release", "season", "episode"},
		},
		{
			name: "tvmaze wins over imdb", query: search.Query{TVMazeID: "42", IMDBID: "tt1234567"}, ok: true,
			want: map[string]string{"tvmaze": "42"}, wantNot: []string{"imdb"},
		},
		{
			name: "imdb normalized", query: search.Query{IMDBID: "1234567"}, ok: true,
			want: map[string]string{"imdb": "tt1234567"},
		},
		{
			name: "episode search", query: search.Query{Keywords: "Example Show", Season: "2", Ep: "3"}, ok: true,
			want: map[string]string{"release": "Example Show", "season": "2", "episode": "3"},
		},
		{
			name: "daily search", query: search.Query{Keywords: "Example Show", Season: "2026", Ep: "07/13"}, ok: true,
			want:    map[string]string{"name": "Example Show", "release": "2026.07.13"},
			wantNot: []string{"season", "episode"},
		},
		{
			name: "deep page", query: search.Query{Keywords: "Example", Limit: 50, Offset: 150}, ok: true,
			want: map[string]string{"per_page": "50", "page": "3"},
		},
		{name: "two character search rejected", query: search.Query{Keywords: "ab"}, ok: false},
		{name: "season without series rejected", query: search.Query{Season: "2", Ep: "3"}, ok: false},
	}

	driver := liveDriver(t, &scriptDoer{handler: func(*stdhttp.Request) (*stdhttp.Response, error) {
		return response(stdhttp.StatusOK, `{"total_results":0,"items":[]}`), nil
	}})
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			rawURL, ok := driver.buildSearchURL(test.query)
			if ok != test.ok {
				t.Fatalf("ok = %v, want %v", ok, test.ok)
			}
			if !ok {
				return
			}
			parsed, err := url.Parse(rawURL)
			if err != nil {
				t.Fatalf("parse URL: %v", err)
			}
			query := parsed.Query()
			if query.Get("api_key") != testAPIKey {
				t.Error("api_key query value mismatch")
			}
			for key, want := range test.want {
				if query.Get(key) != want {
					t.Errorf("query[%q] = %q, want %q", key, query.Get(key), want)
				}
			}
			for _, key := range test.wantNot {
				if query.Has(key) {
					t.Errorf("query unexpectedly contains %q", key)
				}
			}
		})
	}
}

func TestSearchRequestAndNoOp(t *testing.T) {
	t.Parallel()
	doer := &scriptDoer{handler: func(*stdhttp.Request) (*stdhttp.Response, error) {
		return response(stdhttp.StatusOK, `{"total_results":0,"items":[]}`), nil
	}}
	driver := liveDriver(t, doer)

	if _, err := driver.Search(context.Background(), search.Query{Keywords: "Example Show"}); err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(doer.reqs) != 1 {
		t.Fatalf("requests = %d, want 1", len(doer.reqs))
	}
	if doer.reqs[0].method != stdhttp.MethodGet || doer.reqs[0].accept != "application/json" {
		t.Error("search must issue GET with Accept: application/json")
	}
	if doer.reqs[0].url.Path != "/api.php" {
		t.Errorf("request path = %q, want /api.php", doer.reqs[0].url.Path)
	}

	if releases, err := driver.Search(context.Background(), search.Query{Keywords: "ab"}); err != nil || len(releases) != 0 {
		t.Errorf("short search = (%d releases, %v), want empty success", len(releases), err)
	}
	if len(doer.reqs) != 1 {
		t.Errorf("short search issued a request")
	}
}

func TestSearchOffsetWindows(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		offset    int
		wantFirst string
		wantLast  string
		wantPages []string
	}{
		{name: "offset zero", wantFirst: "Release 0", wantLast: "Release 49", wantPages: []string{""}},
		{name: "aligned deep offset", offset: 150, wantFirst: "Release 150", wantLast: "Release 199", wantPages: []string{"3"}},
		{name: "unaligned offset", offset: 125, wantFirst: "Release 125", wantLast: "Release 174", wantPages: []string{"2", "3"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			doer := &scriptDoer{handler: func(req *stdhttp.Request) (*stdhttp.Response, error) {
				page := 0
				if rawPage := req.URL.Query().Get("page"); rawPage != "" {
					parsed, err := strconv.Atoi(rawPage)
					if err != nil {
						t.Fatalf("page query: %v", err)
					}
					page = parsed
				}
				return response(stdhttp.StatusOK, searchPageResponse(t, page*50, 50)), nil
			}}
			driver := liveDriver(t, doer)

			releases, err := driver.Search(context.Background(), search.Query{Keywords: "Example", Offset: tt.offset, Limit: 50})
			if err != nil {
				t.Fatalf("Search: %v", err)
			}
			if len(releases) != 50 {
				t.Fatalf("releases = %d, want 50", len(releases))
			}
			if releases[0].Title != tt.wantFirst || releases[49].Title != tt.wantLast {
				t.Errorf("release window = %q..%q, want %q..%q", releases[0].Title, releases[49].Title, tt.wantFirst, tt.wantLast)
			}
			if got := requestedPages(doer.reqs); !slices.Equal(got, tt.wantPages) {
				t.Errorf("requested pages = %v, want %v", got, tt.wantPages)
			}
		})
	}
}

func searchPageResponse(t *testing.T, start, count int) string {
	t.Helper()
	items := make([]apiRow, count)
	for i := range items {
		id := start + i
		items[i] = apiRow{
			ReleaseTitle: fmt.Sprintf("Release %d", id),
			TorrentID:    int64(id),
			PublishUTC:   "2026-07-13 00:00:00",
		}
	}
	body, err := json.Marshal(apiResponse{TotalResults: 200, Items: &items})
	if err != nil {
		t.Fatalf("marshal page response: %v", err)
	}
	return string(body)
}

func requestedPages(requests []recordedRequest) []string {
	pages := make([]string, 0, len(requests))
	for _, request := range requests {
		pages = append(pages, request.url.Query().Get("page"))
	}
	return pages
}

func TestSearchStatusDispatch(t *testing.T) {
	t.Parallel()
	for _, status := range []int{stdhttp.StatusUnauthorized, stdhttp.StatusForbidden} {
		driver := liveDriver(t, &scriptDoer{handler: func(*stdhttp.Request) (*stdhttp.Response, error) {
			return response(status, "nope"), nil
		}})
		_, err := driver.Search(context.Background(), search.Query{})
		assertActionableAuthError(t, err)
	}

	driver := liveDriver(t, &scriptDoer{handler: func(*stdhttp.Request) (*stdhttp.Response, error) {
		resp := response(stdhttp.StatusTooManyRequests, "nope")
		resp.Header.Set("Retry-After", "120")
		return resp, nil
	}})
	_, err := driver.Search(context.Background(), search.Query{})
	var rateLimited *search.RateLimitedError
	if !errors.As(err, &rateLimited) {
		t.Fatalf("429: err = %v, want RateLimitedError", err)
	}
	if rateLimited.StatusCode != stdhttp.StatusTooManyRequests || rateLimited.RetryAfter != 2*time.Minute {
		t.Errorf("rate-limit metadata = (%d, %s), want (429, 2m)", rateLimited.StatusCode, rateLimited.RetryAfter)
	}
}

func assertActionableAuthError(t *testing.T, err error) {
	t.Helper()
	if !errors.Is(err, login.ErrLoginFailed) {
		t.Fatalf("err = %v, want login.ErrLoginFailed", err)
	}
	for _, semantic := range []string{"unauthorized", "non-interactive", "verify or replace", "API key"} {
		if !strings.Contains(err.Error(), semantic) {
			t.Errorf("auth error %q missing semantic %q", err, semantic)
		}
	}
}

type errorDoer struct{ err error }

func (d *errorDoer) Do(*stdhttp.Request) (*stdhttp.Response, error) { return nil, d.err }

func TestSearchTransportErrorRedactsAPIKey(t *testing.T) {
	t.Parallel()
	transportErr := &url.Error{
		Op:  "Get",
		URL: defaultBaseURL + "api.php?api_key=" + testAPIKey,
		Err: errors.New("connection refused"),
	}
	driver := liveDriver(t, &errorDoer{err: transportErr})
	_, err := driver.Search(context.Background(), search.Query{})
	if err == nil {
		t.Fatal("want transport error")
	}
	if strings.Contains(err.Error(), testAPIKey) || strings.Contains(err.Error(), "api_key=") {
		t.Error("transport error leaked API credentials")
	}
}
