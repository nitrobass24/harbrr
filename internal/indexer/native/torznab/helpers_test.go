package torznab

import (
	stdhttp "net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	apphttp "github.com/autobrr/harbrr/internal/http"
)

// testAPIKey is a synthetic, correctly-sized (32-char) apikey that exists only to
// prove redaction — it never reaches a real server.
const testAPIKey = "SECRETtorznabapikey1234567890ABC"

func fixedClock() time.Time { return time.Unix(1_700_000_000, 0).UTC() }

// redact routes a raw URL through harbrr's RedactURL chokepoint (which redacts the
// apikey query param), mirroring what every log/error site does.
func redact(raw string) string { return apphttp.RedactURL(raw) }

// assertNoAPIKey fails the test if the synthetic apikey appears anywhere in s.
func assertNoAPIKey(t *testing.T, label, s string) {
	t.Helper()
	if strings.Contains(s, testAPIKey) {
		t.Errorf("%s leaked the apikey: %q", label, s)
	}
}

func readGolden(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read golden %q: %v", name, err)
	}
	return b
}

// recordedReq captures one issued request for assertions a black-box transport cannot
// make (the URL — which carries the apikey — and the method/headers).
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

// errorDoer fails every request with a transport error that echoes the URL, so a test
// can prove an error never leaks the apikey-bearing link.
type errorDoer struct{ err error }

func (e *errorDoer) Do(*stdhttp.Request) (*stdhttp.Response, error) { return nil, e.err }
