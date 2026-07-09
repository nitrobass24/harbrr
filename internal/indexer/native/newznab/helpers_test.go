package newznab

import (
	stdhttp "net/http"
	"strings"
	"testing"

	apphttp "github.com/autobrr/harbrr/internal/http"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/normalizer"
)

// releaseT aliases the normalized release so the test files read tersely.
type releaseT = normalizer.Release

// redact routes a raw URL through harbrr's RedactURL chokepoint (which redacts the apikey
// query param), mirroring what every log/error site does.
func redact(raw string) string { return apphttp.RedactURL(raw) }

// assertNoApikey fails the test if the synthetic apikey appears anywhere in s. The label
// names the surface being checked (URL, error, body) so a failure points at the leak site.
func assertNoApikey(t *testing.T, label, s string) {
	t.Helper()
	if strings.Contains(s, testAPIKey) {
		t.Errorf("%s leaked the apikey: %q", label, s)
	}
}

// recordedReq captures one issued request for assertions a black-box transport cannot make
// (the URL — which carries the apikey — and the method/headers).
type recordedReq struct {
	method, url, accept string
}

// scriptDoer records every request and serves a scripted response.
type scriptDoer struct {
	handler func(req *stdhttp.Request) *stdhttp.Response
	reqs    []recordedReq
}

func (s *scriptDoer) Do(req *stdhttp.Request) (*stdhttp.Response, error) {
	s.reqs = append(s.reqs, recordedReq{
		method: req.Method,
		url:    req.URL.String(),
		accept: req.Header.Get("Accept"),
	})
	return s.handler(req), nil
}

// errorDoer fails every request with a transport error that echoes the URL, so a test can
// prove an error never leaks the apikey-bearing link.
type errorDoer struct{ err error }

func (e *errorDoer) Do(*stdhttp.Request) (*stdhttp.Response, error) { return nil, e.err }

// bodyErrReadCloser is a 200 response body that fails partway through Read, simulating a
// mid-body transport failure (timeout/reset) after the headers arrived.
type bodyErrReadCloser struct{ err error }

func (b bodyErrReadCloser) Read([]byte) (int, error) { return 0, b.err }
func (bodyErrReadCloser) Close() error               { return nil }

// bodyErrDoer returns a 200 whose body read fails, so a test can exercise the io.ReadAll
// error path (distinct from a transport error on Do).
type bodyErrDoer struct{ readErr error }

func (d *bodyErrDoer) Do(*stdhttp.Request) (*stdhttp.Response, error) {
	return &stdhttp.Response{
		StatusCode: stdhttp.StatusOK,
		Body:       bodyErrReadCloser{err: d.readErr},
		Header:     stdhttp.Header{},
	}, nil
}
