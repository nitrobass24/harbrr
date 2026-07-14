package torznab

import (
	"context"
	"errors"
	"io"
	stdhttp "net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/login"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
	"github.com/autobrr/harbrr/internal/indexer/native"
)

// stubServerDriver wires a driver to an offline httptest server that serves the given
// status/body and records the request URL it saw (so a test can assert the apikey
// reached the server but never a log).
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
		Def:     presetDefinition(presets[0]),
		Cfg:     map[string]string{"apikey": testAPIKey},
		Doer:    srv.Client(),
		BaseURL: srv.URL,
		Clock:   fixedClock,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return d.(*driver), srv
}

// TestSearchAgainstStub proves Search drives the offline server, parses the response,
// and that the apikey reached the server query while never appearing in the parsed
// result.
func TestSearchAgainstStub(t *testing.T) {
	t.Parallel()
	var sawURL string
	d, _ := stubServerDriver(t, stdhttp.StatusOK, string(readGolden(t, "torznab_morethantv.xml")), &sawURL)

	releases, err := d.Search(context.Background(), search.Query{Mode: "movie-search", IMDBID: "tt0039689"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(releases) != 2 {
		t.Fatalf("releases = %d, want 2", len(releases))
	}
	if !strings.Contains(sawURL, "apikey="+testAPIKey) {
		t.Errorf("server did not receive the apikey; saw %q", redact(sawURL))
	}
	if !strings.Contains(sawURL, "t=movie") || !strings.Contains(sawURL, "imdbid=tt0039689") {
		t.Errorf("server request = %q, want t=movie&imdbid=tt0039689", redact(sawURL))
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

// TestSearchForbidden proves an HTTP 403 ALSO surfaces as a login failure (torznab
// follows ClassifyAuth403 — the majority dialect — unlike the newznab sibling's
// ClassifyRateLimit403).
func TestSearchForbidden(t *testing.T) {
	t.Parallel()
	d, _ := stubServerDriver(t, stdhttp.StatusForbidden, "denied", nil)
	_, err := d.Search(context.Background(), search.Query{})
	if !errors.Is(err, login.ErrLoginFailed) {
		t.Fatalf("err = %v, want login.ErrLoginFailed", err)
	}
}

// TestSearchRateLimited proves an HTTP 503 surfaces as a rate-limit error (registry
// backs off).
func TestSearchRateLimited(t *testing.T) {
	t.Parallel()
	d, _ := stubServerDriver(t, stdhttp.StatusServiceUnavailable, "busy", nil)
	_, err := d.Search(context.Background(), search.Query{})
	if !errors.Is(err, search.ErrRateLimited) {
		t.Fatalf("err = %v, want a rate-limit error", err)
	}
}

// TestSearchNonXMLBodyIsAuthFailure proves a 2xx response whose body does not start
// with "<" (an HTML login/error page, say) is Jackett's MoreThanTVAPI non-XML guard —
// surfaced as a login failure WITHOUT ever including the body content in the error.
func TestSearchNonXMLBodyIsAuthFailure(t *testing.T) {
	t.Parallel()
	d, _ := stubServerDriver(t, stdhttp.StatusOK, "Access denied for key "+testAPIKey, nil)
	_, err := d.Search(context.Background(), search.Query{})
	if !errors.Is(err, login.ErrLoginFailed) {
		t.Fatalf("err = %v, want login.ErrLoginFailed", err)
	}
	if strings.Contains(err.Error(), testAPIKey) {
		t.Fatalf("err = %q, must never echo the response body (may reflect the apikey)", err.Error())
	}
	if strings.Contains(err.Error(), "Access denied") {
		t.Fatalf("err = %q, must never include body content at all", err.Error())
	}
}

// TestCheckXMLBody unit-tests the body-shape guard directly: a body starting with "<"
// (after trimming whitespace) passes; anything else — including an empty body — fails
// as a login error with no body content echoed.
func TestCheckXMLBody(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		body string
		ok   bool
	}{
		{"xml", `<?xml version="1.0"?><rss></rss>`, true},
		{"leading whitespace xml", "   \n\t<rss></rss>", true},
		{"empty", "", false},
		{"whitespace only", "   \n\t", false},
		{"html error page", "<!DOCTYPE html><html>nope</html>", true}, // starts with "<" -> passed to the XML decoder, which will itself fail
		{"plain text error", "Access denied", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			err := checkXMLBody([]byte(c.body))
			if c.ok && err != nil {
				t.Errorf("checkXMLBody(%q) = %v, want nil", c.body, err)
			}
			if !c.ok {
				if !errors.Is(err, login.ErrLoginFailed) {
					t.Errorf("checkXMLBody(%q) = %v, want login.ErrLoginFailed", c.body, err)
				}
				if strings.Contains(err.Error(), c.body) && c.body != "" {
					t.Errorf("checkXMLBody error echoed the body: %q", err.Error())
				}
			}
		})
	}
}

// TestSearchTransportErrorRedactsAPIKey proves a real *url.Error transport failure —
// whose URL echoes the apikey in a query param — surfaces only the endpoint's
// scheme://host.
func TestSearchTransportErrorRedactsAPIKey(t *testing.T) {
	t.Parallel()
	const baseURL = "https://torznab.example.test"
	uerr := &url.Error{
		Op:  "Get",
		URL: baseURL + "/api/torznab?apikey=" + testAPIKey,
		Err: errors.New("dial tcp: connection refused"),
	}
	d, err := New(native.Params{
		Def:     presetDefinition(presets[0]),
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
		t.Errorf("error dropped the endpoint host; got %q", got)
	}
	assertNoAPIKey(t, "search transport error", got)
}
