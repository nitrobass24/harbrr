package gazelle

import (
	"context"
	"errors"
	"io"
	stdhttp "net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/login"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
	"github.com/autobrr/harbrr/internal/indexer/native"
)

// recordedReq captures one issued request for assertions a black-box transport cannot
// otherwise make: the URL (which must NEVER carry the apikey) and the Authorization
// header (which carries the per-site-prefixed apikey).
type recordedReq struct {
	method, url, authorization, accept string
}

// scriptDoer records every request and serves a single scripted response.
type scriptDoer struct {
	resp *stdhttp.Response
	reqs []recordedReq
}

func (s *scriptDoer) Do(req *stdhttp.Request) (*stdhttp.Response, error) {
	s.reqs = append(s.reqs, recordedReq{
		method:        req.Method,
		url:           req.URL.String(),
		authorization: req.Header.Get("Authorization"),
		accept:        req.Header.Get("Accept"),
	})
	if s.resp != nil {
		return s.resp, nil
	}
	return mkResp(stdhttp.StatusOK, `{"status":"success","response":{"results":[],"currentPage":"1","pages":"1"}}`), nil
}

func mkResp(status int, body string) *stdhttp.Response {
	return &stdhttp.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: stdhttp.Header{}}
}

// searchDriver wires a driver for one site to a scriptDoer with the synthetic apikey.
func searchDriver(t *testing.T, id string, doer search.Doer) *driver {
	t.Helper()
	def := familyByID(t, id).Definition
	d, err := New(native.Params{
		Def:   def,
		Cfg:   map[string]string{"apikey": credAPIKey},
		Doer:  doer,
		Clock: func() time.Time { return fixedClock },
	})
	if err != nil {
		t.Fatalf("New(%q): %v", id, err)
	}
	return d.(*driver)
}

// TestBuildBrowseURL is the parity gate for the browse request URL: it asserts the exact
// URL harbrr emits per query shape against the Phase-1 confirmed contract (action/order
// always set; searchstr/artistname/groupname/year fielded; filter_cat[<id>]=1 per tracker
// category; no recordlabel; VA artist skipped) for both sites. The base URL differs per
// site but the query string is identical, so each case is checked against both.
func TestBuildBrowseURL(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		query search.Query
		// want is the query string after "ajax.php?" (the filter_cat tail, if any, is
		// appended raw after url.Values.Encode()).
		want string
	}{
		{
			name:  "empty -> RSS/browse",
			query: search.Query{},
			want:  "action=browse&order_by=time&order_way=desc",
		},
		{
			name:  "keyword with dots -> spaces",
			query: search.Query{Keywords: "daft.punk discovery"},
			want:  "action=browse&order_by=time&order_way=desc&searchstr=daft+punk+discovery",
		},
		{
			name:  "artist+album+year",
			query: search.Query{Artist: "Daft Punk", Album: "Discovery", Year: "2001"},
			want:  "action=browse&artistname=Daft+Punk&groupname=Discovery&order_by=time&order_way=desc&year=2001",
		},
		{
			name:  "VA artist skipped",
			query: search.Query{Artist: "VA", Album: "Discovery"},
			want:  "action=browse&groupname=Discovery&order_by=time&order_way=desc",
		},
		{
			name:  "single category",
			query: search.Query{Categories: []string{"1"}},
			want:  "action=browse&order_by=time&order_way=desc&filter_cat%5B1%5D=1",
		},
		{
			name:  "multiple categories deduped in order",
			query: search.Query{Categories: []string{"1", "4", "1"}},
			want:  "action=browse&order_by=time&order_way=desc&filter_cat%5B1%5D=1&filter_cat%5B4%5D=1",
		},
		{
			name:  "artist+album+category",
			query: search.Query{Artist: "Daft Punk", Album: "Discovery", Year: "2001", Categories: []string{"1"}},
			want:  "action=browse&artistname=Daft+Punk&groupname=Discovery&order_by=time&order_way=desc&year=2001&filter_cat%5B1%5D=1",
		},
		{
			name:  "label is NOT sent as recordlabel",
			query: search.Query{Album: "Discovery", Label: "Virgin"},
			want:  "action=browse&groupname=Discovery&order_by=time&order_way=desc",
		},
	}

	sites := map[string]string{
		"redacted": "https://redacted.sh/ajax.php?",
		"orpheus":  "https://orpheus.network/ajax.php?",
	}
	for _, tc := range cases {
		for site, prefix := range sites {
			t.Run(tc.name+"/"+site, func(t *testing.T) {
				t.Parallel()
				d := searchDriver(t, site, &scriptDoer{})
				got := d.buildBrowseURL(tc.query)
				want := prefix + tc.want
				if got != want {
					t.Errorf("buildBrowseURL()\n got=%q\nwant=%q", got, want)
				}
				if strings.Contains(got, credAPIKey) {
					t.Errorf("browse URL leaks the apikey: %q", got)
				}
			})
		}
	}
}

// TestSearchAuthHeaderPerSite proves the Authorization header is the per-site value (RED
// bare apikey, OPS "token "-prefixed), that the apikey is present in the header but NEVER
// in the recorded URL, and that Accept advertises JSON.
func TestSearchAuthHeaderPerSite(t *testing.T) {
	t.Parallel()
	cases := []struct {
		site    string
		wantHdr string
	}{
		{site: "redacted", wantHdr: credAPIKey},
		{site: "orpheus", wantHdr: "token " + credAPIKey},
	}
	for _, tc := range cases {
		t.Run(tc.site, func(t *testing.T) {
			t.Parallel()
			doer := &scriptDoer{}
			d := searchDriver(t, tc.site, doer)
			if _, err := d.Search(context.Background(), search.Query{Keywords: "x"}); err != nil {
				t.Fatalf("Search: %v", err)
			}
			if len(doer.reqs) != 1 {
				t.Fatalf("want 1 request, got %d", len(doer.reqs))
			}
			req := doer.reqs[0]
			if req.method != stdhttp.MethodGet {
				t.Errorf("method = %q, want GET", req.method)
			}
			if req.authorization != tc.wantHdr {
				t.Errorf("Authorization = %q, want %q", req.authorization, tc.wantHdr)
			}
			if req.accept != "application/json" {
				t.Errorf("Accept = %q, want application/json", req.accept)
			}
			if !strings.Contains(req.authorization, credAPIKey) {
				t.Errorf("Authorization must carry the apikey, got %q", req.authorization)
			}
			if strings.Contains(req.url, credAPIKey) {
				t.Errorf("URL must NOT carry the apikey, got %q", req.url)
			}
		})
	}
}

// TestSearchSuccessReturnsReleases proves a 200 status:"success" body flows through
// parseBrowse into normalized releases.
func TestSearchSuccessReturnsReleases(t *testing.T) {
	t.Parallel()
	body := readFixture(t, "testdata/browse_music.json")
	doer := &scriptDoer{resp: mkResp(stdhttp.StatusOK, string(body))}
	d := searchDriver(t, "redacted", doer)
	rels, err := d.Search(context.Background(), search.Query{Keywords: "logistics"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(rels) == 0 {
		t.Fatalf("want releases, got 0")
	}
}

// TestSearchAuthFailure proves a 401/403 maps to login.ErrLoginFailed and never leaks the
// apikey in the error.
func TestSearchAuthFailure(t *testing.T) {
	t.Parallel()
	for _, code := range []int{stdhttp.StatusUnauthorized, stdhttp.StatusForbidden} {
		doer := &scriptDoer{resp: mkResp(code, "")}
		d := searchDriver(t, "redacted", doer)
		_, err := d.Search(context.Background(), search.Query{})
		if !errors.Is(err, login.ErrLoginFailed) {
			t.Fatalf("HTTP %d: want login.ErrLoginFailed, got %v", code, err)
		}
		if strings.Contains(err.Error(), credAPIKey) {
			t.Errorf("error leaks the apikey: %v", err)
		}
	}
}

// TestSearchTransportErrorHostOnly proves the changed browse transport wrap (get() in
// auth.go) surfaces only the endpoint's scheme://host and drops the path+query when the
// doer fails with a real *url.Error. The fabricated *url.Error hides a secret in BOTH a
// path segment and a query param on the driver's own host; SchemeHost/RedactURLError keep
// the host (safe to diagnose, not a secret) but strip the path and query, so neither the
// fabricated URL secret nor the configured apikey can leak. RED's real browse URL carries
// no secret (auth is the header), so the fabricated URL secret is what proves the drop.
func TestSearchTransportErrorHostOnly(t *testing.T) {
	t.Parallel()
	const secret = "S3CRETTOKEN"
	uerr := &url.Error{
		Op:  "Get",
		URL: "https://redacted.sh/dl/" + secret + "?passkey=" + secret,
		Err: errors.New("dial tcp: connection refused"),
	}
	d := searchDriver(t, "redacted", &errDoer{err: uerr})

	_, err := d.Search(context.Background(), search.Query{Keywords: "logistics"})
	if err == nil {
		t.Fatal("Search should error on a transport failure")
	}
	msg := err.Error()
	if !strings.Contains(msg, "https://redacted.sh") {
		t.Errorf("error should surface the scheme://host (the host is not a secret), got %q", msg)
	}
	for _, leak := range []string{secret, "/dl/" + secret, "passkey=" + secret, credAPIKey} {
		if strings.Contains(msg, leak) {
			t.Errorf("transport error leaks %q: %q", leak, msg)
		}
	}
}

// TestSearchRateLimited proves a 429 surfaces a RateLimitedError carrying the status and
// Retry-After.
func TestSearchRateLimited(t *testing.T) {
	t.Parallel()
	resp := mkResp(stdhttp.StatusTooManyRequests, "")
	resp.Header.Set("Retry-After", "12")
	doer := &scriptDoer{resp: resp}
	d := searchDriver(t, "orpheus", doer)
	_, err := d.Search(context.Background(), search.Query{})
	var rl *search.RateLimitedError
	if !errors.As(err, &rl) {
		t.Fatalf("want RateLimitedError, got %v", err)
	}
	if rl.StatusCode != stdhttp.StatusTooManyRequests {
		t.Errorf("StatusCode = %d, want 429", rl.StatusCode)
	}
	if rl.RetryAfter != 12*time.Second {
		t.Errorf("RetryAfter = %v, want 12s", rl.RetryAfter)
	}
}

// TestSearchServerError proves a non-2xx, non-auth, non-rate-limit status is a plain
// error.
func TestSearchServerError(t *testing.T) {
	t.Parallel()
	doer := &scriptDoer{resp: mkResp(stdhttp.StatusInternalServerError, "boom")}
	d := searchDriver(t, "redacted", doer)
	_, err := d.Search(context.Background(), search.Query{})
	if err == nil {
		t.Fatalf("want error on HTTP 500")
	}
	if errors.Is(err, login.ErrLoginFailed) {
		t.Errorf("500 should not be a login failure: %v", err)
	}
}
