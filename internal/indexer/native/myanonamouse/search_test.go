package myanonamouse

import (
	"context"
	"errors"
	stdhttp "net/http"
	"net/url"
	"reflect"
	"testing"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/login"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
)

// builderDriver is a credential-light driver for the pure query-string builder tests.
func builderDriver(cfg map[string]string) *driver {
	if cfg == nil {
		cfg = map[string]string{}
	}
	return &driver{cfg: cfg, baseURL: "https://mam.test/", clock: fixedClock}
}

// TestBuildSearchURL is the parity gate for the request: it asserts the exact query
// string harbrr emits against Prowlarr's MyAnonamouseRequestGenerator contract — the
// keyword in tor[text], the always-on title/author/narrator search-in flags, the
// constant searchType/searchIn/sortType, the categories (or "0"), perpage/startNumber,
// and the thumbnails/description/dlLink flags.
func TestBuildSearchURL(t *testing.T) {
	t.Parallel()
	const endpoint = "https://mam.test/tor/js/loadSearchJSONbasic.php"
	cases := []struct {
		name  string
		cfg   map[string]string
		query search.Query
		want  url.Values
	}{
		{
			name:  "basic keyword, no category -> cat 0",
			query: search.Query{Keywords: "dune"},
			want: url.Values{
				"tor[text]":             {"dune"},
				"tor[searchType]":       {"all"},
				"tor[searchIn]":         {"torrents"},
				"tor[sortType]":         {"default"},
				"tor[srchIn][title]":    {"true"},
				"tor[srchIn][author]":   {"true"},
				"tor[srchIn][narrator]": {"true"},
				"tor[cat][]":            {"0"},
				"tor[perpage]":          {"100"},
				"tor[startNumber]":      {"0"},
				"thumbnails":            {"1"},
				"description":           {"1"},
				"dlLink":                {"1"},
			},
		},
		{
			name:  "categories -> indexed tor[cat][n]",
			query: search.Query{Keywords: "asimov", Categories: []string{"13", "14"}},
			want: url.Values{
				"tor[text]":             {"asimov"},
				"tor[searchType]":       {"all"},
				"tor[searchIn]":         {"torrents"},
				"tor[sortType]":         {"default"},
				"tor[srchIn][title]":    {"true"},
				"tor[srchIn][author]":   {"true"},
				"tor[srchIn][narrator]": {"true"},
				"tor[cat][0]":           {"13"},
				"tor[cat][1]":           {"14"},
				"tor[perpage]":          {"100"},
				"tor[startNumber]":      {"0"},
				"thumbnails":            {"1"},
				"description":           {"1"},
				"dlLink":                {"1"},
			},
		},
		{
			name:  "search-in toggles add the optional srchIn flags",
			cfg:   map[string]string{"search_in_description": "True", "search_in_series": "1", "search_in_filenames": "on"},
			query: search.Query{Keywords: "weir"},
			want: url.Values{
				"tor[text]":                {"weir"},
				"tor[searchType]":          {"all"},
				"tor[searchIn]":            {"torrents"},
				"tor[sortType]":            {"default"},
				"tor[srchIn][title]":       {"true"},
				"tor[srchIn][author]":      {"true"},
				"tor[srchIn][narrator]":    {"true"},
				"tor[srchIn][description]": {"true"},
				"tor[srchIn][series]":      {"true"},
				"tor[srchIn][filenames]":   {"true"},
				"tor[cat][]":               {"0"},
				"tor[perpage]":             {"100"},
				"tor[startNumber]":         {"0"},
				"thumbnails":               {"1"},
				"description":              {"1"},
				"dlLink":                   {"1"},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			raw := builderDriver(tc.cfg).buildSearchURL(tc.query)
			u, err := url.Parse(raw)
			if err != nil {
				t.Fatalf("parse %q: %v", raw, err)
			}
			if got := u.Scheme + "://" + u.Host + u.Path; got != endpoint {
				t.Errorf("endpoint = %q, want %q", got, endpoint)
			}
			if got := u.Query(); !reflect.DeepEqual(map[string][]string(got), map[string][]string(tc.want)) {
				t.Errorf("query =\n  %v\nwant\n  %v", got, tc.want)
			}
		})
	}
}

// TestSearchIssuesCookieRequest proves Search drives the built URL with the mam_id
// Cookie + Accept: application/json header attached, and that the served (recorded)
// URL leaks no secret (the mam_id rides only in the Cookie header).
func TestSearchIssuesCookieRequest(t *testing.T) {
	t.Parallel()
	doer := &scriptDoer{handler: func(_ *stdhttp.Request) *stdhttp.Response {
		return resp(stdhttp.StatusOK, `{"error":"","data":[]}`)
	}}
	d := newDriver(doer)
	if _, err := d.Search(context.Background(), search.Query{Keywords: "dune", Categories: []string{"13"}}); err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(doer.reqs) != 1 {
		t.Fatalf("requests = %d, want 1", len(doer.reqs))
	}
	got := doer.reqs[0]
	if got.method != stdhttp.MethodGet {
		t.Errorf("method = %q, want GET", got.method)
	}
	if got.cookie != "mam_id="+mamSecret {
		t.Errorf("Cookie = %q, want the mam_id", got.cookie)
	}
	if got.accept != "application/json" {
		t.Errorf("Accept = %q, want application/json", got.accept)
	}
	u, _ := url.Parse(got.url)
	if u.Query().Get("tor[text]") != "dune" || u.Query().Get("tor[cat][0]") != "13" {
		t.Errorf("recorded query = %v", u.Query())
	}
	assertNoSecret(t, got.url)
}

// TestSearchStatusDispatch proves Search maps the response status: 403 is an auth
// failure (login.ErrLoginFailed), 429 is a rate-limit error.
func TestSearchStatusDispatch(t *testing.T) {
	t.Parallel()
	mk := func(status int) *driver {
		return newDriver(&scriptDoer{handler: func(_ *stdhttp.Request) *stdhttp.Response {
			return resp(status, "nope")
		}})
	}

	_, err := mk(stdhttp.StatusForbidden).Search(context.Background(), search.Query{Keywords: "x"})
	if !errors.Is(err, login.ErrLoginFailed) {
		t.Errorf("403: err = %v, want login.ErrLoginFailed", err)
	}
	assertNoSecret(t, err.Error())

	_, err = mk(stdhttp.StatusTooManyRequests).Search(context.Background(), search.Query{Keywords: "x"})
	var rl *search.RateLimitedError
	if !errors.As(err, &rl) {
		t.Errorf("429: err = %v, want *search.RateLimitedError", err)
	}
}

func TestDistinctNonEmpty(t *testing.T) {
	t.Parallel()
	got := distinctNonEmpty([]string{"13", "", "14", "13", " 15 "})
	want := []string{"13", "14", "15"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("distinctNonEmpty = %v, want %v", got, want)
	}
}

func TestBoolSetting(t *testing.T) {
	t.Parallel()
	for _, v := range []string{"True", "true", "1", "on", "yes"} {
		if !boolSetting(v) {
			t.Errorf("boolSetting(%q) = false, want true", v)
		}
	}
	for _, v := range []string{"", "false", "0", "no"} {
		if boolSetting(v) {
			t.Errorf("boolSetting(%q) = true, want false", v)
		}
	}
}
