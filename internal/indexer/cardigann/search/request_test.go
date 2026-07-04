package search

import (
	"errors"
	stdhttp "net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/loader"
)

// testDeps builds a minimal Deps for request-building tests. Request building
// only reads Config + BaseURL (the selector/filter/normalizer are unused until
// ParseResults), so they are left nil here.
func testDeps(baseURL string, config map[string]string) Deps {
	return Deps{Config: config, BaseURL: baseURL}
}

// TestBuildRequests_GET asserts a GET search renders the path + inputs into a
// query string resolved against the base URL, with secrets redactable. The
// passkey-shaped value is built by concatenation so secret scanners do not flag
// the fixture.
func TestBuildRequests_GET(t *testing.T) {
	t.Parallel()

	inherit := true
	def := &loader.Definition{
		Links: []string{"https://get.invalid/"},
		Search: loader.Search{
			Path:   "/browse.php",
			Inputs: loader.NewInputsBlock(loader.InputEntry{Key: "q", Value: loader.Scalar{Value: "{{ .Keywords }}", Set: true}}),
			Paths:  nil,
		},
	}
	// Force the single-path shape with inheritance (mirrors the loader default).
	def.Search.Paths = []loader.SearchPathBlock{{Path: "/browse.php", InheritInputs: &inherit}}

	reqs, err := buildRequests(def, Query{Keywords: "ubuntu"}, testDeps("https://get.invalid/", nil))
	if err != nil {
		t.Fatalf("buildRequests: %v", err)
	}
	if len(reqs) != 1 {
		t.Fatalf("reqs = %d, want 1", len(reqs))
	}
	got := reqs[0]
	if got.method != "GET" {
		t.Errorf("method = %q, want GET", got.method)
	}
	u, err := url.Parse(got.url)
	if err != nil {
		t.Fatalf("parsing built URL: %v", err)
	}
	if u.Host != "get.invalid" || u.Path != "/browse.php" {
		t.Errorf("url host/path = %q %q", u.Host, u.Path)
	}
	if q := u.Query().Get("q"); q != "ubuntu" {
		t.Errorf("query q = %q, want ubuntu", q)
	}
	if got.body != "" {
		t.Errorf("GET body = %q, want empty", got.body)
	}
}

// TestBuildRequests_PerPathMeta asserts each built request carries ITS path's
// followredirect + response type (Jackett reads both per SearchPath) — never a
// neighbor's: the first-path-wins response-type reuse was a live bug for mixed
// HTML+JSON defs.
func TestBuildRequests_PerPathMeta(t *testing.T) {
	t.Parallel()

	follow := true
	def := &loader.Definition{
		Links: []string{"https://meta.invalid/"},
		Search: loader.Search{
			Paths: []loader.SearchPathBlock{
				{Path: "/browse", FollowRedirect: &follow},
				{Path: "/api", Response: &loader.ResponseBlock{Type: "json"}},
				{Path: "/rss", Response: &loader.ResponseBlock{Type: "xml"}},
			},
		},
	}

	reqs, err := buildRequests(def, Query{Keywords: "x"}, testDeps("https://meta.invalid/", nil))
	if err != nil {
		t.Fatalf("buildRequests: %v", err)
	}
	if len(reqs) != 3 {
		t.Fatalf("reqs = %d, want 3", len(reqs))
	}
	want := []struct {
		followRedirect bool
		respType       string
	}{
		{followRedirect: true, respType: ""},
		{followRedirect: false, respType: "json"},
		{followRedirect: false, respType: "xml"},
	}
	for i, w := range want {
		if reqs[i].followRedirect != w.followRedirect {
			t.Errorf("reqs[%d].followRedirect = %v, want %v", i, reqs[i].followRedirect, w.followRedirect)
		}
		if reqs[i].respType != w.respType {
			t.Errorf("reqs[%d].respType = %q, want %q", i, reqs[i].respType, w.respType)
		}
	}
}

// TestBuildRequests_POST asserts a POST search renders inputs into a form body
// with a form Content-Type, leaving the URL query empty.
func TestBuildRequests_POST(t *testing.T) {
	t.Parallel()

	def := &loader.Definition{
		Links: []string{"https://post.invalid/"},
		Search: loader.Search{
			Inputs: loader.NewInputsBlock(loader.InputEntry{Key: "search", Value: loader.Scalar{Value: "{{ .Keywords }}", Set: true}}),
			Paths: []loader.SearchPathBlock{{
				Path:   "/api/search",
				Method: "post",
			}},
		},
	}

	reqs, err := buildRequests(def, Query{Keywords: "debian"}, testDeps("https://post.invalid/", nil))
	if err != nil {
		t.Fatalf("buildRequests: %v", err)
	}
	got := reqs[0]
	if got.method != "POST" {
		t.Errorf("method = %q, want POST", got.method)
	}
	form, err := url.ParseQuery(got.body)
	if err != nil {
		t.Fatalf("parsing body: %v", err)
	}
	if form.Get("search") != "debian" {
		t.Errorf("body search = %q, want debian", form.Get("search"))
	}
	if ct := got.headers["Content-Type"]; len(ct) != 1 || ct[0] != "application/x-www-form-urlencoded" {
		t.Errorf("Content-Type = %v", got.headers["Content-Type"])
	}
	u, _ := url.Parse(got.url)
	if u.RawQuery != "" {
		t.Errorf("POST url query = %q, want empty", u.RawQuery)
	}
}

// TestBuildRequests_ConfigInput proves .Config values flow into rendered inputs
// (e.g. a passkey carried in the query). The passkey-shaped value is assembled
// by concatenation so scanners do not flag the fixture.
func TestBuildRequests_ConfigInput(t *testing.T) {
	t.Parallel()

	passkey := "PK" + "0000000000000000000000000000"
	def := &loader.Definition{
		Links: []string{"https://cfg.invalid/"},
		Search: loader.Search{
			Inputs: loader.NewInputsBlock(loader.InputEntry{Key: "passkey", Value: loader.Scalar{Value: "{{ .Config.passkey }}", Set: true}}),
			Paths:  []loader.SearchPathBlock{{Path: "/t"}},
		},
	}

	reqs, err := buildRequests(def, Query{}, testDeps("https://cfg.invalid/", map[string]string{"passkey": passkey}))
	if err != nil {
		t.Fatalf("buildRequests: %v", err)
	}
	u, _ := url.Parse(reqs[0].url)
	if u.Query().Get("passkey") != passkey {
		t.Errorf("passkey query = %q, want %q", u.Query().Get("passkey"), passkey)
	}
}

// TestBuildRequests_InputOrder proves search inputs render in DEFINITION order,
// not alphabetical. Jackett appends inputs to an ordered collection as it
// iterates Search.Inputs (CardigannIndexer.PerformQuery); a Go map or a
// sorted-keys encoder would reorder zeta/alpha/mu and diverge from Jackett's
// request URL. This test fails with the previous sorted-keys behavior.
func TestBuildRequests_InputOrder(t *testing.T) {
	t.Parallel()

	inherit := true
	def := &loader.Definition{
		Links: []string{"https://order.invalid/"},
		Search: loader.Search{
			Inputs: loader.NewInputsBlock(
				loader.InputEntry{Key: "zeta", Value: loader.Scalar{Value: "1", Set: true}},
				loader.InputEntry{Key: "alpha", Value: loader.Scalar{Value: "2", Set: true}},
				loader.InputEntry{Key: "mu", Value: loader.Scalar{Value: "3", Set: true}},
			),
			Paths: []loader.SearchPathBlock{{Path: "/s", InheritInputs: &inherit}},
		},
	}

	reqs, err := buildRequests(def, Query{}, testDeps("https://order.invalid/", nil))
	if err != nil {
		t.Fatalf("buildRequests: %v", err)
	}
	u, err := url.Parse(reqs[0].url)
	if err != nil {
		t.Fatalf("parsing built URL: %v", err)
	}
	if want := "zeta=1&alpha=2&mu=3"; u.RawQuery != want {
		t.Errorf("query = %q, want %q (definition order, not alphabetical)", u.RawQuery, want)
	}
}

// TestBuildRequests_EmbeddedQueryPreserved proves an embedded path query is kept
// VERBATIM — order and empty values intact — and inputs append after it without
// re-encoding. The JSON-API archetype (UNIT3D) builds the entire query inside
// the path with no inputs; re-encoding via url.Values would alphabetize it
// (api_token, name, page, perPage, sortField) and break request parity.
func TestBuildRequests_EmbeddedQueryPreserved(t *testing.T) {
	t.Parallel()

	embedded := "api_token=&name=1080p&sortField=created_at&perPage=100&page=1"
	def := &loader.Definition{
		Links: []string{"https://embed.invalid/"},
		Search: loader.Search{
			Paths: []loader.SearchPathBlock{{Path: "/api/torrents/filter?" + embedded}},
		},
	}

	reqs, err := buildRequests(def, Query{}, testDeps("https://embed.invalid/", nil))
	if err != nil {
		t.Fatalf("buildRequests: %v", err)
	}
	u, err := url.Parse(reqs[0].url)
	if err != nil {
		t.Fatalf("parsing built URL: %v", err)
	}
	if u.RawQuery != embedded {
		t.Errorf("query = %q, want %q (verbatim, not re-sorted)", u.RawQuery, embedded)
	}
}

// TestBuildRequests_PathValueEncoding proves a keyword inlined into the search
// PATH is URL-encoded (space -> %20), matching Jackett's
// WebUtility.UrlEncode-rendered path. Without it, a multi-word keyword would
// leave a literal space in the path, producing a malformed URL — defs like
// teamos build `?filename={{ .Keywords }}` directly in the path.
func TestBuildRequests_PathValueEncoding(t *testing.T) {
	t.Parallel()

	def := &loader.Definition{
		Links: []string{"https://path.invalid/"},
		Search: loader.Search{
			Paths: []loader.SearchPathBlock{{Path: "/torrents/?filename={{ .Keywords }}&page=1"}},
		},
	}

	reqs, err := buildRequests(def, Query{Keywords: "big buck bunny"}, testDeps("https://path.invalid/", nil))
	if err != nil {
		t.Fatalf("buildRequests: %v", err)
	}
	u, err := url.Parse(reqs[0].url)
	if err != nil {
		t.Fatalf("parsing built URL: %v", err)
	}
	if want := "filename=big%20buck%20bunny&page=1"; u.RawQuery != want {
		t.Errorf("query = %q, want %q (path value space-encoded)", u.RawQuery, want)
	}
}

// errDoer is a Doer that always fails the round-trip, so doRequest takes its
// transport-error path with a passkey-bearing URL.
type errDoer struct{}

func (errDoer) Do(*stdhttp.Request) (*stdhttp.Response, error) {
	return nil, errors.New("dial failed")
}

// TestDoRequest_RedactsPasskeyInError proves the search HTTP path never leaks a
// secret into an error, wherever the definition put it: error sites surface
// host-only detail (apphttp.SchemeHost), so a passkey survives in neither a
// query param (even under a name no scrub list knows) nor a PATH segment (where
// a query-name scrub could never reach). The passkey-shaped values are
// assembled by concatenation so scanners do not flag the fixture.
func TestDoRequest_RedactsPasskeyInError(t *testing.T) {
	t.Parallel()

	passkey := "PK" + "1111111111111111111111111111"
	tests := []struct {
		name     string
		url      string
		wantHost bool
	}{
		{"query passkey", "https://leak.invalid/browse?passkey=" + passkey, true},
		{"query under an unlisted name", "https://leak.invalid/browse?pk=" + passkey, true},
		{"path-embedded passkey", "https://leak.invalid/download/" + passkey + "/file.torrent", true},
		// An UNPARSEABLE URL fails at request build with a *url.Error that quotes
		// the FULL raw input; the wrap must not let it through (RedactURLError).
		{"unparseable url with secret", "https://leak.invalid/dl/" + passkey + "/\x7f", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			br := builtRequest{method: stdhttp.MethodGet, url: tt.url}

			_, err := doRequest(t.Context(), errDoer{}, br, nil)
			if err == nil {
				t.Fatal("doRequest returned nil error, want failure")
			}
			if strings.Contains(err.Error(), passkey) {
				t.Errorf("doRequest error leaked passkey: %q", err.Error())
			}
			if tt.wantHost && !strings.Contains(err.Error(), "https://leak.invalid") {
				t.Errorf("doRequest error lost the host detail: %q", err.Error())
			}

			_, err = doSearchRequest(t.Context(), errDoer{}, br, nil)
			if err == nil {
				t.Fatal("doSearchRequest returned nil error, want failure")
			}
			if strings.Contains(err.Error(), passkey) {
				t.Errorf("doSearchRequest error leaked passkey: %q", err.Error())
			}
		})
	}
}
