package torrentday

import (
	"io"
	stdhttp "net/http"
	"strings"
	"testing"
	"time"

	"github.com/autobrr/harbrr/internal/indexer/native"
)

// Distinctive synthetic secrets so a redaction check can prove they never escape into a
// recorded URL or an error. These exist only in test code, per the secret-scanning
// carve-out. The cookie is the session secret (uid=...; pass=...) that rides only in the
// Cookie request header.
const (
	credCookie = "uid=12345; pass=SECRET-deadbeefdeadbeefdeadbeefdeadbeef"
	credUA     = "Mozilla/5.0 (harbrr-test-UA-7c3d)"
)

// recordedReq captures one issued request for assertions a black-box transport cannot
// make (the Cookie / User-Agent headers).
type recordedReq struct {
	method, url, cookie, userAgent, accept string
}

// scriptDoer records every request and serves a scripted response.
type scriptDoer struct {
	handler func(req *stdhttp.Request) *stdhttp.Response
	reqs    []recordedReq
}

func (s *scriptDoer) Do(req *stdhttp.Request) (*stdhttp.Response, error) {
	s.reqs = append(s.reqs, recordedReq{
		method:    req.Method,
		url:       req.URL.String(),
		cookie:    req.Header.Get("Cookie"),
		userAgent: req.Header.Get("User-Agent"),
		accept:    req.Header.Get("Accept"),
	})
	return s.handler(req), nil
}

func resp(status int, body string) *stdhttp.Response {
	return &stdhttp.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: stdhttp.Header{}}
}

// redirectResp builds a 3xx response whose Location points at location (no body needed —
// the transport surfaces the redirect rather than following it).
func redirectResp(status int, location string) *stdhttp.Response {
	h := stdhttp.Header{}
	h.Set("Location", location)
	return &stdhttp.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader("")), Header: h}
}

// fixedClock pins "now" so a Retry-After delta is deterministic.
func fixedClock() time.Time { return time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC) }

// testDriver builds a credential-bearing driver wired to doer, with the fixed clock. cfg
// defaults to the synthetic cookie + UA so the redaction assertions have a secret to
// scrub.
func testDriver(t *testing.T, doer *scriptDoer, cfg map[string]string) *driver {
	t.Helper()
	if cfg == nil {
		cfg = map[string]string{"cookie": credCookie, "user_agent": credUA}
	}
	d, err := New(native.Params{Def: Families()[0].Definition, Cfg: cfg, Doer: doer})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	dr := d.(*driver)
	dr.clock = fixedClock
	return dr
}

// assertNoSecret fails if s leaks any synthetic credential.
func assertNoSecret(t *testing.T, s string) {
	t.Helper()
	for _, secret := range []string{credCookie, credUA, "SECRET-deadbeefdeadbeefdeadbeefdeadbeef", "pass=SECRET"} {
		if strings.Contains(s, secret) {
			t.Errorf("string leaks a credential (%q): %q", secret, s)
		}
	}
}
