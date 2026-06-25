package newznab

import (
	"context"
	"errors"
	"io"
	stdhttp "net/http"
	"net/http/httptest"
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

// TestSearchTransportErrorRedactsApikey proves a transport failure (whose *url.Error echoes
// the full apikey-bearing request URL) never leaks the apikey in the surfaced error.
func TestSearchTransportErrorRedactsApikey(t *testing.T) {
	t.Parallel()
	doer := &errorDoer{err: errors.New("dial tcp: connection refused")}
	d, err := New(native.Params{
		Def:     GenericDefinition(),
		Cfg:     map[string]string{"apikey": testAPIKey},
		Doer:    doer,
		BaseURL: "https://news.example.test",
		Clock:   fixedClock,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, searchErr := d.Search(context.Background(), search.Query{Keywords: "x"})
	if searchErr == nil {
		t.Fatal("Search err = nil, want a transport error")
	}
	assertNoApikey(t, "search transport error", searchErr.Error())
}

// TestTestMethod proves Test() issues an empty search and reports a clean 200 (even an empty
// feed) as success, and a 401 as a login failure.
func TestTestMethod(t *testing.T) {
	t.Parallel()
	empty := `<?xml version="1.0"?><rss><channel></channel></rss>`
	d, _ := stubServerDriver(t, stdhttp.StatusOK, empty, nil)
	if err := d.Test(context.Background()); err != nil {
		t.Fatalf("Test (clean) = %v, want nil", err)
	}

	bad, _ := stubServerDriver(t, stdhttp.StatusUnauthorized, "no", nil)
	if err := bad.Test(context.Background()); !errors.Is(err, login.ErrLoginFailed) {
		t.Fatalf("Test (bad creds) = %v, want login.ErrLoginFailed", err)
	}
}
