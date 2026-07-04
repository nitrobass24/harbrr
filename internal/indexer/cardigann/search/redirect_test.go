package search

import (
	"errors"
	"io"
	stdhttp "net/http"
	"strconv"
	"strings"
	"testing"

	apphttp "github.com/autobrr/harbrr/internal/http"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/loader"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/login"
)

// redirectStep is one scripted exchange for redirectDoer: the expected request
// (method + URL) and the canned response (status, optional Location, body).
type redirectStep struct {
	wantMethod string
	wantURL    string
	status     int
	location   string
	body       string
}

// redirectDoer serves an ordered redirect chain, asserting each request matches
// the script and recording it for header/body assertions.
type redirectDoer struct {
	t        *testing.T
	steps    []redirectStep
	requests []recordedReq
}

func (d *redirectDoer) Do(req *stdhttp.Request) (*stdhttp.Response, error) {
	var body string
	if req.Body != nil {
		b, _ := io.ReadAll(req.Body)
		body = string(b)
	}
	d.requests = append(d.requests, recordedReq{req.Method, req.URL.String(), body, req.Header.Clone()})

	i := len(d.requests) - 1
	if i >= len(d.steps) {
		d.t.Fatalf("redirectDoer: unexpected request %d: %s %s", i+1, req.Method, req.URL)
	}
	step := d.steps[i]
	if req.Method != step.wantMethod || req.URL.String() != step.wantURL {
		d.t.Fatalf("redirectDoer: request %d = %s %s, want %s %s", i+1, req.Method, req.URL, step.wantMethod, step.wantURL)
	}
	status := step.status
	if status == 0 {
		status = stdhttp.StatusOK
	}
	header := stdhttp.Header{}
	if step.location != "" {
		header.Set("Location", step.location)
	}
	return &stdhttp.Response{
		StatusCode: status,
		Header:     header,
		Body:       io.NopCloser(strings.NewReader(step.body)),
		Request:    req,
	}, nil
}

func TestDoSearchRequest_RedirectSurfacedAsData(t *testing.T) {
	t.Parallel()
	doer := &redirectDoer{t: t, steps: []redirectStep{
		{wantMethod: "GET", wantURL: "https://r.test/browse", status: stdhttp.StatusFound, location: "/moved?x=1", body: "redirect body"},
	}}
	sr, err := doSearchRequest(t.Context(), doer, builtRequest{method: stdhttp.MethodGet, url: "https://r.test/browse"}, nil)
	if err != nil {
		t.Fatalf("doSearchRequest: %v", err)
	}
	if sr.status != stdhttp.StatusFound {
		t.Errorf("status = %d, want 302", sr.status)
	}
	// The relative Location is resolved against the request URL even though the
	// fake Doer set resp.Request (resolution never depends on it).
	if want := "https://r.test/moved?x=1"; sr.location != want {
		t.Errorf("location = %q, want %q", sr.location, want)
	}
	if string(sr.body) != "redirect body" {
		t.Errorf("body = %q, want the redirect body", sr.body)
	}
}

// TestDoSearchRequest_NeverAutoFollowed drives a REAL *http.Client carrying the
// production RedirectPolicy: the no-follow stamp doSearchRequest applies must
// stop the client from consuming the 302 itself. Exactly one request reaches
// the transport, and the 3xx comes back as data.
func TestDoSearchRequest_NeverAutoFollowed(t *testing.T) {
	t.Parallel()
	rt := &redirectDoer{t: t, steps: []redirectStep{
		{wantMethod: "GET", wantURL: "https://r.test/browse", status: stdhttp.StatusMovedPermanently, location: "https://r.test/next"},
	}}
	client := &stdhttp.Client{Transport: roundTripFunc(rt.Do), CheckRedirect: apphttp.RedirectPolicy}
	sr, err := doSearchRequest(t.Context(), client, builtRequest{method: stdhttp.MethodGet, url: "https://r.test/browse"}, nil)
	if err != nil {
		t.Fatalf("doSearchRequest: %v", err)
	}
	if sr.status != stdhttp.StatusMovedPermanently {
		t.Errorf("status = %d, want 301 surfaced raw", sr.status)
	}
	if len(rt.requests) != 1 {
		t.Errorf("transport saw %d requests, want 1 (no auto-follow)", len(rt.requests))
	}
}

// roundTripFunc adapts a Do-shaped func into an http.RoundTripper.
type roundTripFunc func(*stdhttp.Request) (*stdhttp.Response, error)

func (f roundTripFunc) RoundTrip(req *stdhttp.Request) (*stdhttp.Response, error) { return f(req) }

// TestDoRequest_DownloadPathStillFollows pins the seam split: doRequest — the
// download/grab request path — issues UNSTAMPED requests, so a client carrying
// the production RedirectPolicy still auto-follows a 302 (Jackett's download
// flow always follows). Only doSearchRequest opts out.
func TestDoRequest_DownloadPathStillFollows(t *testing.T) {
	t.Parallel()
	rt := &redirectDoer{t: t, steps: []redirectStep{
		{wantMethod: "GET", wantURL: "https://r.test/dl/1", status: stdhttp.StatusFound, location: "/dl/real.torrent"},
		{wantMethod: "GET", wantURL: "https://r.test/dl/real.torrent", body: "torrent bytes"},
	}}
	client := &stdhttp.Client{Transport: roundTripFunc(rt.Do), CheckRedirect: apphttp.RedirectPolicy}
	body, err := doRequest(t.Context(), client, builtRequest{method: stdhttp.MethodGet, url: "https://r.test/dl/1"}, nil)
	if err != nil {
		t.Fatalf("doRequest: %v", err)
	}
	if string(body) != "torrent bytes" {
		t.Fatalf("body = %q, want the followed target", body)
	}
	if len(rt.requests) != 2 {
		t.Errorf("transport saw %d requests, want 2 (client followed the 302)", len(rt.requests))
	}
}

func TestFollowRedirects_FollowsChainAsBareGET(t *testing.T) {
	t.Parallel()
	doer := &redirectDoer{t: t, steps: []redirectStep{
		{wantMethod: "POST", wantURL: "https://r.test/browse", status: stdhttp.StatusFound, location: "/hop1"},
		{wantMethod: "GET", wantURL: "https://r.test/hop1", status: stdhttp.StatusFound, location: "https://mirror.test/hop2"},
		{wantMethod: "GET", wantURL: "https://mirror.test/hop2", body: "final page"},
	}}
	session := &login.Session{UserAgent: "solver-ua/1.0"}
	br := builtRequest{
		method:         stdhttp.MethodPost,
		url:            "https://r.test/browse",
		body:           "q=x",
		headers:        map[string][]string{"X-Custom": {"def-header"}},
		followRedirect: true,
	}
	first, err := doSearchRequest(t.Context(), doer, br, session)
	if err != nil {
		t.Fatalf("doSearchRequest: %v", err)
	}
	final, err := followRedirects(t.Context(), doer, first, session)
	if err != nil {
		t.Fatalf("followRedirects: %v", err)
	}
	if final.status != stdhttp.StatusOK || string(final.body) != "final page" {
		t.Fatalf("final = %d %q, want 200 %q", final.status, final.body, "final page")
	}
	// Hops are bare GETs: no method/body/definition-header carry-over (Jackett's
	// redirect WebRequest carries only cookies), but the session UA IS re-applied
	// (a cf_clearance-bound session breaks without it).
	for _, hop := range doer.requests[1:] {
		if hop.body != "" {
			t.Errorf("hop %s carried body %q, want none", hop.url, hop.body)
		}
		if got := hop.headers.Get("X-Custom"); got != "" {
			t.Errorf("hop %s carried definition header %q, want none", hop.url, got)
		}
		if got := hop.headers.Get("User-Agent"); got != "solver-ua/1.0" {
			t.Errorf("hop %s User-Agent = %q, want the session UA", hop.url, got)
		}
	}
}

func TestFollowRedirects_HopCapLeavesRedirect(t *testing.T) {
	t.Parallel()
	steps := []redirectStep{
		{wantMethod: "GET", wantURL: "https://r.test/browse", status: stdhttp.StatusFound, location: "/hop1"},
	}
	for i := 1; i <= maxRedirectHops; i++ {
		steps = append(steps, redirectStep{
			wantMethod: "GET",
			wantURL:    "https://r.test/hop" + strconv.Itoa(i),
			status:     stdhttp.StatusFound,
			location:   "/hop" + strconv.Itoa(i+1),
		})
	}
	doer := &redirectDoer{t: t, steps: steps}
	first, err := doSearchRequest(t.Context(), doer, builtRequest{method: stdhttp.MethodGet, url: "https://r.test/browse"}, nil)
	if err != nil {
		t.Fatalf("doSearchRequest: %v", err)
	}
	final, err := followRedirects(t.Context(), doer, first, nil)
	if err != nil {
		t.Fatalf("followRedirects: %v", err)
	}
	if !isRedirectStatus(final.status) {
		t.Fatalf("final status = %d, want a still-redirect after the %d-hop cap", final.status, maxRedirectHops)
	}
	if got := len(doer.requests); got != 1+maxRedirectHops {
		t.Errorf("requests = %d, want initial + %d hops", got, maxRedirectHops)
	}
}

func TestFollowRedirects_MagnetStops(t *testing.T) {
	t.Parallel()
	doer := &redirectDoer{t: t, steps: []redirectStep{
		{wantMethod: "GET", wantURL: "https://r.test/browse", status: stdhttp.StatusFound, location: "magnet:?xt=urn:btih:abc"},
	}}
	first, err := doSearchRequest(t.Context(), doer, builtRequest{method: stdhttp.MethodGet, url: "https://r.test/browse"}, nil)
	if err != nil {
		t.Fatalf("doSearchRequest: %v", err)
	}
	final, err := followRedirects(t.Context(), doer, first, nil)
	if err != nil {
		t.Fatalf("followRedirects: %v", err)
	}
	if final.status != stdhttp.StatusFound {
		t.Errorf("final status = %d, want the redirect handed back intact", final.status)
	}
	if len(doer.requests) != 1 {
		t.Errorf("requests = %d, want 1 (magnet target is never fetched)", len(doer.requests))
	}
}

func TestFollowRedirects_UnsupportedSchemeErrors(t *testing.T) {
	t.Parallel()
	doer := &redirectDoer{t: t, steps: []redirectStep{
		{wantMethod: "GET", wantURL: "https://r.test/browse", status: stdhttp.StatusFound, location: "ftp://r.test/file"},
	}}
	first, err := doSearchRequest(t.Context(), doer, builtRequest{method: stdhttp.MethodGet, url: "https://r.test/browse"}, nil)
	if err != nil {
		t.Fatalf("doSearchRequest: %v", err)
	}
	if _, err := followRedirects(t.Context(), doer, first, nil); err == nil {
		t.Fatal("followRedirects: want error for a non-http(s), non-magnet scheme")
	}
}

func TestFollowRedirects_NoLocationStops(t *testing.T) {
	t.Parallel()
	doer := &redirectDoer{t: t, steps: []redirectStep{
		{wantMethod: "GET", wantURL: "https://r.test/browse", status: stdhttp.StatusFound},
	}}
	first, err := doSearchRequest(t.Context(), doer, builtRequest{method: stdhttp.MethodGet, url: "https://r.test/browse"}, nil)
	if err != nil {
		t.Fatalf("doSearchRequest: %v", err)
	}
	final, err := followRedirects(t.Context(), doer, first, nil)
	if err != nil {
		t.Fatalf("followRedirects: %v", err)
	}
	if final.status != stdhttp.StatusFound || len(doer.requests) != 1 {
		t.Errorf("final = %d after %d requests, want the Location-less 302 back untouched", final.status, len(doer.requests))
	}
}

func TestResolveRedirect_Mapping(t *testing.T) {
	t.Parallel()
	withLogin := &loader.Definition{ID: "r", Login: &loader.Login{Method: "get"}}
	noLogin := &loader.Definition{ID: "r"}
	redirect := searchResponse{status: stdhttp.StatusFound, location: "https://r.test/login", body: []byte("moved")}

	t.Run("login def -> logged-out signal", func(t *testing.T) {
		t.Parallel()
		_, err := resolveRedirect(t.Context(), &redirectDoer{t: t}, builtRequest{}, redirect, withLogin, nil)
		if !errors.Is(err, ErrSearchLoggedOut) {
			t.Fatalf("err = %v, want ErrSearchLoggedOut", err)
		}
	})
	t.Run("no-login def -> one re-request, second response parsed", func(t *testing.T) {
		t.Parallel()
		// Jackett's CheckIfLoginIsNeeded fires even without a login block: DoLogin
		// no-ops, the search is re-requested ONCE, and the second response is
		// parsed (the 302's Set-Cookie already rides the client jar, so a
		// cookie-gate tracker succeeds on this retry).
		doer := &redirectDoer{t: t, steps: []redirectStep{
			{wantMethod: "GET", wantURL: "https://r.test/browse", body: "second response"},
		}}
		br := builtRequest{method: stdhttp.MethodGet, url: "https://r.test/browse"}
		sr, err := resolveRedirect(t.Context(), doer, br, redirect, noLogin, nil)
		if err != nil {
			t.Fatalf("resolveRedirect: %v", err)
		}
		if string(sr.body) != "second response" {
			t.Errorf("body = %q, want the re-requested response", sr.body)
		}
		if len(doer.requests) != 1 {
			t.Errorf("re-requests = %d, want exactly 1 (bounded, never a loop)", len(doer.requests))
		}
	})
	t.Run("no-login def -> re-request still 302 parsed as-is", func(t *testing.T) {
		t.Parallel()
		doer := &redirectDoer{t: t, steps: []redirectStep{
			{wantMethod: "GET", wantURL: "https://r.test/browse", status: stdhttp.StatusFound, location: "/login", body: "still moved"},
		}}
		br := builtRequest{method: stdhttp.MethodGet, url: "https://r.test/browse"}
		sr, err := resolveRedirect(t.Context(), doer, br, redirect, noLogin, nil)
		if err != nil {
			t.Fatalf("resolveRedirect: %v", err)
		}
		if string(sr.body) != "still moved" || len(doer.requests) != 1 {
			t.Errorf("body = %q after %d re-requests, want the second 302 body after exactly 1 (Jackett parses the second response as-is)", sr.body, len(doer.requests))
		}
	})
	t.Run("xml path -> redirect body parsed as-is even with login", func(t *testing.T) {
		t.Parallel()
		// Jackett's XML branch never runs CheckIfLoginIsNeeded: no relogin, no
		// re-request — the redirect body goes straight to the XML parser.
		sr, err := resolveRedirect(t.Context(), &redirectDoer{t: t}, builtRequest{respType: responseTypeXML}, redirect, withLogin, nil)
		if err != nil {
			t.Fatalf("resolveRedirect: %v", err)
		}
		if string(sr.body) != "moved" {
			t.Errorf("body = %q, want the redirect body handed back for XML parsing", sr.body)
		}
	})
	t.Run("follow exhausted + login def -> logged-out signal", func(t *testing.T) {
		t.Parallel()
		doer := &redirectDoer{t: t, steps: []redirectStep{
			{wantMethod: "GET", wantURL: "https://r.test/a", status: stdhttp.StatusFound, location: "/b"},
			{wantMethod: "GET", wantURL: "https://r.test/b", status: stdhttp.StatusFound, location: "/c"},
			{wantMethod: "GET", wantURL: "https://r.test/c", status: stdhttp.StatusFound, location: "/d"},
			{wantMethod: "GET", wantURL: "https://r.test/d", status: stdhttp.StatusFound, location: "/e"},
			{wantMethod: "GET", wantURL: "https://r.test/e", status: stdhttp.StatusFound, location: "/f"},
		}}
		first := searchResponse{status: stdhttp.StatusFound, location: "https://r.test/a"}
		_, err := resolveRedirect(t.Context(), doer, builtRequest{followRedirect: true}, first, withLogin, nil)
		if !errors.Is(err, ErrSearchLoggedOut) {
			t.Fatalf("err = %v, want ErrSearchLoggedOut after the hop cap", err)
		}
	})
	t.Run("follow lands -> followed response returned", func(t *testing.T) {
		t.Parallel()
		doer := &redirectDoer{t: t, steps: []redirectStep{
			{wantMethod: "GET", wantURL: "https://r.test/moved", body: "landed"},
		}}
		first := searchResponse{status: stdhttp.StatusFound, location: "https://r.test/moved"}
		sr, err := resolveRedirect(t.Context(), doer, builtRequest{followRedirect: true}, first, withLogin, nil)
		if err != nil {
			t.Fatalf("resolveRedirect: %v", err)
		}
		if string(sr.body) != "landed" {
			t.Errorf("body = %q, want the followed page", sr.body)
		}
	})
}
