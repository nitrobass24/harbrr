package newznab

import (
	"context"
	"errors"
	"io"
	stdhttp "net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/login"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
	"github.com/autobrr/harbrr/internal/indexer/native"
)

func fixedClock() time.Time { return time.Unix(1_700_000_000, 0).UTC() }

// stubServerDriver wires a driver to an offline httptest server that serves the given body
// and records the request URL it saw (so the test can assert the apikey reached the server
// but never a log). The server's base URL becomes the driver's BaseURL.
func stubServerDriver(t *testing.T, status int, body string, sawURL *string) (*driver, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		if sawURL != nil {
			*sawURL = r.URL.String()
		}
		w.Header().Set("Content-Type", "application/rss+xml")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	d, err := New(native.Params{
		Def:     GenericDefinition(),
		Cfg:     map[string]string{"apikey": testAPIKey, "apiPath": "/api"},
		Doer:    srv.Client(),
		BaseURL: srv.URL,
		Clock:   fixedClock,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return d.(*driver), srv
}

// TestSearchAgainstStub proves Search drives the offline server, parses the response, and
// that the apikey reached the server query (so it authenticates) while never appearing in
// the result links' parsed form (the enclosure URL is the server's, not harbrr's apikey).
func TestSearchAgainstStub(t *testing.T) {
	t.Parallel()
	var sawURL string
	d, _ := stubServerDriver(t, stdhttp.StatusOK, string(readGolden(t, "search.xml")), &sawURL)

	releases, err := d.Search(context.Background(), search.Query{Mode: "movie-search", IMDBID: "tt0133093"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(releases) != 2 {
		t.Fatalf("releases = %d, want 2", len(releases))
	}
	if !strings.Contains(sawURL, "apikey="+testAPIKey) {
		t.Errorf("server did not receive the apikey; saw %q", redact(sawURL))
	}
	if !strings.Contains(sawURL, "t=movie") || !strings.Contains(sawURL, "imdbid=0133093") {
		t.Errorf("server request = %q, want t=movie&imdbid=0133093", redact(sawURL))
	}
}

// TestSearchUnauthorized proves an HTTP 401 surfaces as a login failure.
func TestSearchUnauthorized(t *testing.T) {
	t.Parallel()
	d, _ := stubServerDriver(t, stdhttp.StatusUnauthorized, "denied", nil)
	_, err := d.Search(context.Background(), search.Query{})
	if !errors.Is(err, login.ErrLoginFailed) {
		t.Fatalf("err = %v, want login.ErrLoginFailed", err)
	}
}

// TestSearchRateLimited proves an HTTP 503 surfaces as a rate-limit error (registry backs
// off).
func TestSearchRateLimited(t *testing.T) {
	t.Parallel()
	d, _ := stubServerDriver(t, stdhttp.StatusServiceUnavailable, "busy", nil)
	_, err := d.Search(context.Background(), search.Query{})
	if !errors.Is(err, search.ErrRateLimited) {
		t.Fatalf("err = %v, want a rate-limit error", err)
	}
}

// TestSearchTransportErrorRedactsApikey proves a real *url.Error transport failure — whose
// URL echoes the apikey in BOTH a PATH segment and a query param — surfaces only the
// endpoint's scheme://host: the host survives (it is not a secret and is enough to diagnose)
// while the apikey, its /dl/<secret> path, and its apikey=<secret> query are all dropped by
// the get() wrap (apphttp.SchemeHost + apphttp.RedactURLError).
func TestSearchTransportErrorRedactsApikey(t *testing.T) {
	t.Parallel()
	const baseURL = "https://news.example.test"
	// A stdlib http.Client.Do failure is always a *url.Error quoting the full request URL;
	// fabricate one that hides the apikey in a path segment and a query param, exercising
	// both leak surfaces SchemeHost/RedactURLError must scrub.
	uerr := &url.Error{
		Op:  "Get",
		URL: baseURL + "/dl/" + testAPIKey + "?apikey=" + testAPIKey,
		Err: errors.New("dial tcp: connection refused"),
	}
	d, err := New(native.Params{
		Def:     GenericDefinition(),
		Cfg:     map[string]string{"apikey": testAPIKey},
		Doer:    &errorDoer{err: uerr},
		BaseURL: baseURL,
		Clock:   fixedClock,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, searchErr := d.Search(context.Background(), search.Query{Keywords: "x"})
	if searchErr == nil {
		t.Fatal("Search err = nil, want a transport error")
	}
	got := searchErr.Error()
	if !strings.Contains(got, baseURL) {
		t.Errorf("error dropped the endpoint host; got %q, want it to contain %q", got, baseURL)
	}
	if strings.Contains(got, "/dl/"+testAPIKey) {
		t.Errorf("error leaked the secret path segment: %q", got)
	}
	if strings.Contains(got, "apikey="+testAPIKey) {
		t.Errorf("error leaked the secret query param: %q", got)
	}
	assertNoApikey(t, "search transport error", got)
}

// TestTestMethod proves Test() primes the caps cache: a clean 200 serving a valid <caps>
// document is success (and the caps are now cached), while a 401 is a login failure.
func TestTestMethod(t *testing.T) {
	t.Parallel()
	d, _ := stubServerDriver(t, stdhttp.StatusOK, minimalCaps, nil)
	if err := d.Test(context.Background()); err != nil {
		t.Fatalf("Test (clean) = %v, want nil", err)
	}
	if _, ok := d.capsCache.get(fixedClock()); !ok {
		t.Error("Test did not prime the caps cache")
	}

	bad, _ := stubServerDriver(t, stdhttp.StatusUnauthorized, "no", nil)
	if err := bad.Test(context.Background()); !errors.Is(err, login.ErrLoginFailed) {
		t.Fatalf("Test (bad creds) = %v, want login.ErrLoginFailed", err)
	}
}

// TestSearchBodyReadErrorSurfacesCause proves a mid-body read failure still classifies as
// ErrParseError (so a health event is recorded — there is no transport health kind) AND
// carries the real read error, instead of a bare "parse_error" with no cause.
func TestSearchBodyReadErrorSurfacesCause(t *testing.T) {
	t.Parallel()
	d, err := New(native.Params{
		Def:     GenericDefinition(),
		Cfg:     map[string]string{"apikey": testAPIKey},
		Doer:    &bodyErrDoer{readErr: errors.New("unexpected EOF reading body")},
		BaseURL: "https://news.example.test",
		Clock:   fixedClock,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, searchErr := d.Search(context.Background(), search.Query{Keywords: "x"})
	if !errors.Is(searchErr, search.ErrParseError) {
		t.Fatalf("err = %v, want ErrParseError (health classification must be preserved)", searchErr)
	}
	if !strings.Contains(searchErr.Error(), "unexpected EOF reading body") {
		t.Fatalf("err = %q, want the real read cause included (not a bare parse_error)", searchErr.Error())
	}
	assertNoApikey(t, "search body-read error", searchErr.Error())
}

// minimalCaps is a tiny valid caps document for the Test() priming check.
const minimalCaps = `<?xml version="1.0"?><caps>` +
	`<searching><search available="yes" supportedParams="q"/></searching>` +
	`<categories><category id="2000" name="Movies"/></categories></caps>`
