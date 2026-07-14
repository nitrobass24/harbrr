package iptorrents

import (
	"io"
	stdhttp "net/http"
	"strings"
	"testing"
	"time"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/mapper"
	"github.com/autobrr/harbrr/internal/indexer/native"
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

// Distinctive synthetic secrets so a redaction check can prove they never escape into a
// recorded URL or an error. These exist only in test code, per the secret-scanning carve-out.
const (
	credCookie = "uid=SECRET-9f8e; pass=SECRET-1234"
	credUA     = "Mozilla/5.0 (harbrr-test-UA-7c3d)"
)

// fixedClock pins "now" so relative-date parsing is deterministic.
func fixedClock() time.Time { return time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC) }

// testDriver builds a credential-bearing driver wired to doer, with the fixed clock.
func testDriver(doer *scriptDoer, cfg map[string]string) *driver {
	if cfg == nil {
		cfg = map[string]string{"cookie": credCookie, "user_agent": credUA}
	}
	return &driver{
		Base: native.Base{
			Family:  "iptorrents",
			Def:     Families()[0].Definition,
			Caps:    iptCapabilities(),
			Cfg:     cfg,
			Doer:    doer,
			BaseURL: "https://iptorrents.com/",
			Clock:   fixedClock,
		},
	}
}

// iptCapabilities builds the IPTorrents caps the way the registry would, for the
// parse/category tests that need MapTrackerCatToNewznab.
func iptCapabilities() *mapper.Capabilities {
	d, err := New(native.Params{Def: Families()[0].Definition})
	if err != nil {
		panic(err)
	}
	return d.Capabilities()
}

// assertNoSecret fails if s leaks either synthetic credential.
func assertNoSecret(t *testing.T, s string) {
	t.Helper()
	for _, secret := range []string{credCookie, credUA, "SECRET-9f8e", "SECRET-1234"} {
		if strings.Contains(s, secret) {
			t.Errorf("string leaks a credential (%q): %q", secret, s)
		}
	}
}
