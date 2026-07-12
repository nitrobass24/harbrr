package nzbindex

import (
	"io"
	stdhttp "net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/normalizer"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
	"github.com/autobrr/harbrr/internal/indexer/native"
)

const (
	testAPIKey  = "s3cr3tnzbindexkey"
	testBaseURL = "https://nzbindex.test"
)

// releaseT aliases the normalized release so the test files read tersely.
type releaseT = normalizer.Release

// recordedReq captures one issued request for assertions a black-box transport cannot make
// (the URL — which may carry the apikey — and the method/Accept header).
type recordedReq struct{ method, url, accept string }

// scriptDoer records every request and serves a scripted response.
type scriptDoer struct {
	handler func(req *stdhttp.Request) *stdhttp.Response
	reqs    []recordedReq
}

func (s *scriptDoer) Do(req *stdhttp.Request) (*stdhttp.Response, error) {
	s.reqs = append(s.reqs, recordedReq{req.Method, req.URL.String(), req.Header.Get("Accept")})
	return s.handler(req), nil
}

// errorDoer fails every request with a transport error (e.g. a *url.Error whose Error()
// echoes the request URL), so a test can prove a grab error surfaces the sentinel without
// leaking the URL.
type errorDoer struct{ err error }

func (e *errorDoer) Do(*stdhttp.Request) (*stdhttp.Response, error) { return nil, e.err }

// jsonResponse builds a 200 application/json response carrying body.
func jsonResponse(body string) *stdhttp.Response {
	return &stdhttp.Response{
		StatusCode: stdhttp.StatusOK,
		Header:     stdhttp.Header{"Content-Type": {"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

// statusResponse builds a response with the given status code and empty body.
func statusResponse(code int) *stdhttp.Response {
	return &stdhttp.Response{StatusCode: code, Header: stdhttp.Header{}, Body: io.NopCloser(strings.NewReader(""))}
}

// testDriver builds a driver with the given settings and transport.
func testDriver(t *testing.T, cfg map[string]string, doer search.Doer) *driver {
	t.Helper()
	d, err := New(native.Params{Def: Definition(), BaseURL: testBaseURL, Cfg: cfg, Doer: doer})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return d.(*driver)
}

// readGolden reads a testdata file.
func readGolden(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read golden %q: %v", name, err)
	}
	return b
}
