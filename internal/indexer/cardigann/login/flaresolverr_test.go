package login

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"compress/zlib"
	"context"
	"encoding/json"
	"io"
	stdhttp "net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// flareStub serves the FlareSolverr /v1 protocol with the given response, capturing
// the decoded request so a test can assert the typed request contract.
func flareStub(t *testing.T, resp flareResponse) (*httptest.Server, *flareRequest) {
	t.Helper()
	captured := &flareRequest{}
	srv := httptest.NewServer(stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		_ = json.NewDecoder(r.Body).Decode(captured)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	t.Cleanup(srv.Close)
	return srv, captured
}

func TestFlareSolverrSolve_Success(t *testing.T) {
	t.Parallel()
	srv, captured := flareStub(t, flareResponse{
		Status:   "ok",
		Solution: flareSolution{UserAgent: "BrowserUA/1.0", Cookies: []flareCookie{{Name: "cf_clearance", Value: "CLR-TOKEN"}}},
	})
	res, err := NewFlareSolverrSolver(srv.URL, 0).Solve(context.Background(), "https://tracker.test/")
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}
	if res.UserAgent != "BrowserUA/1.0" {
		t.Errorf("UA = %q, want the solution UA", res.UserAgent)
	}
	if len(res.Cookies) != 1 || res.Cookies[0].Name != "cf_clearance" || res.Cookies[0].Value != "CLR-TOKEN" {
		t.Errorf("cookies = %+v, want one cf_clearance", res.Cookies)
	}
	if captured.Cmd != "request.get" || captured.URL != "https://tracker.test/" || captured.MaxTimeout <= 0 {
		t.Errorf("request = %+v, want cmd=request.get url=target maxTimeout>0", captured)
	}
}

func TestFlareSolverrSolve_ErrorStatus(t *testing.T) {
	t.Parallel()
	srv, _ := flareStub(t, flareResponse{Status: "error", Message: "Challenge not solved!"})
	if _, err := NewFlareSolverrSolver(srv.URL, 0).Solve(context.Background(), "https://t/"); err == nil {
		t.Fatal("want error when status != ok")
	}
}

func TestFlareSolverrSolve_HTTPError(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, _ *stdhttp.Request) {
		w.WriteHeader(stdhttp.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)
	if _, err := NewFlareSolverrSolver(srv.URL, 0).Solve(context.Background(), "https://t/"); err == nil {
		t.Fatal("want error on HTTP 500")
	}
}

func TestFlareSolverrSolve_NoURL(t *testing.T) {
	t.Parallel()
	if _, err := NewFlareSolverrSolver("", 0).Solve(context.Background(), "https://t/"); err == nil {
		t.Fatal("want error when flaresolverr_url is empty")
	}
}

// replayRecordingDoer serves a challenge then a clean page, recording the replay
// (second) request's headers so the header contract can be asserted.
type replayRecordingDoer struct {
	calls         int
	replayHeaders stdhttp.Header
}

func (d *replayRecordingDoer) Do(req *stdhttp.Request) (*stdhttp.Response, error) {
	d.calls++
	body := "Just a moment..."
	if d.calls >= 2 {
		body = "<html><body>login form</body></html>"
		d.replayHeaders = req.Header.Clone()
	}
	return &stdhttp.Response{
		StatusCode: stdhttp.StatusOK,
		Header:     stdhttp.Header{},
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    req,
	}, nil
}

// TestFlareSolverrReplayHeaderContract is the offline gate: after a solve, the
// replayed request carries the solver's UA AND a browser-realistic, non-gzip-only
// Accept set (it asserts the header contract, NOT real-CF acceptance).
func TestFlareSolverrReplayHeaderContract(t *testing.T) {
	t.Parallel()
	srv, _ := flareStub(t, flareResponse{
		Status:   "ok",
		Solution: flareSolution{UserAgent: "BrowserUA/1.0", Cookies: []flareCookie{{Name: "cf_clearance", Value: "CLR"}}},
	})
	doer := &replayRecordingDoer{}
	e := New(WithClient(doer), WithBaseURL("https://t.invalid/"), WithSolver(NewFlareSolverrSolver(srv.URL, 0)))

	if _, err := e.fetchLandingPastAntiBot(context.Background(), "https://t.invalid/login.php", nil); err != nil {
		t.Fatalf("fetchLandingPastAntiBot: %v", err)
	}
	h := doer.replayHeaders
	if h.Get("User-Agent") != "BrowserUA/1.0" {
		t.Errorf("replay User-Agent = %q, want the solver's UA", h.Get("User-Agent"))
	}
	if !strings.Contains(h.Get("Accept"), "text/html") {
		t.Errorf("replay Accept = %q, want a browser Accept set", h.Get("Accept"))
	}
	if ae := h.Get("Accept-Encoding"); ae == "" || ae == "gzip" {
		t.Errorf("replay Accept-Encoding = %q, want a non-gzip-only set", ae)
	}
	if h.Get("Accept-Language") == "" {
		t.Error("replay missing Accept-Language (browser-realistic header set)")
	}
}

// encodingDoer returns a body with the given Content-Encoding, simulating a server
// honoring the replay's explicit Accept-Encoding.
type encodingDoer struct {
	encoding string
	body     []byte
}

func (d *encodingDoer) Do(req *stdhttp.Request) (*stdhttp.Response, error) {
	h := stdhttp.Header{}
	h.Set("Content-Encoding", d.encoding)
	return &stdhttp.Response{
		StatusCode: stdhttp.StatusOK,
		Header:     h,
		Body:       io.NopCloser(bytes.NewReader(d.body)),
		Request:    req,
	}, nil
}

// TestDo_DecompressesGzip proves do() decompresses a gzip response (needed because
// the solver replay sends an explicit Accept-Encoding, suppressing net/http's
// transparent gzip handling).
func TestDo_DecompressesGzip(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write([]byte("<html>decompressed-ok</html>")); err != nil {
		t.Fatalf("gzip write: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
	e := New(WithClient(&encodingDoer{encoding: "gzip", body: buf.Bytes()}), WithBaseURL("https://t.invalid/"))
	body, _, err := e.get(context.Background(), "https://t.invalid/x", nil)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !strings.Contains(string(body), "decompressed-ok") {
		t.Errorf("body not decompressed: %q", body)
	}
}

// TestDo_DecompressesDeflate proves do() also handles a deflate response.
func TestDo_DecompressesDeflate(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	fw, err := flate.NewWriter(&buf, flate.DefaultCompression)
	if err != nil {
		t.Fatalf("flate writer: %v", err)
	}
	if _, err := fw.Write([]byte("<html>deflate-ok</html>")); err != nil {
		t.Fatalf("flate write: %v", err)
	}
	if err := fw.Close(); err != nil {
		t.Fatalf("flate close: %v", err)
	}
	e := New(WithClient(&encodingDoer{encoding: "deflate", body: buf.Bytes()}), WithBaseURL("https://t.invalid/"))
	body, _, err := e.get(context.Background(), "https://t.invalid/x", nil)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !strings.Contains(string(body), "deflate-ok") {
		t.Errorf("body not decompressed: %q", body)
	}
}

// TestDo_DecompressesZlibDeflate proves do() handles the RFC-9110-compliant
// zlib-wrapped form of Content-Encoding: deflate (not just raw DEFLATE).
func TestDo_DecompressesZlibDeflate(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	zw := zlib.NewWriter(&buf)
	if _, err := zw.Write([]byte("<html>zlib-deflate-ok</html>")); err != nil {
		t.Fatalf("zlib write: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zlib close: %v", err)
	}
	e := New(WithClient(&encodingDoer{encoding: "deflate", body: buf.Bytes()}), WithBaseURL("https://t.invalid/"))
	body, _, err := e.get(context.Background(), "https://t.invalid/x", nil)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !strings.Contains(string(body), "zlib-deflate-ok") {
		t.Errorf("zlib-wrapped deflate not decompressed: %q", body)
	}
}
