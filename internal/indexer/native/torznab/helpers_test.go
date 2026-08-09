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

// testAPIKey is a synthetic apikey that exists only to prove redaction — it never
// reaches a real server.
const testAPIKey = "SECRETtorznabapikey1234567890ABC"

// fixturePreset preserves the shape of MoreThanTV — the site whose Jackett driver
// (MoreThanTVAPI.cs) is this family's parser/request reference, and the source of
// the real captures in testdata/. The site shut down in 2026-08 and its user-facing
// preset was retired, but the captures still gate the driver's behavior for the
// generic entry and the surviving presets, so its shape lives on here: the fixed
// apiPath, a required key, an eight-category pass-through table, the keyInfo hint
// field, and sealed (URL-credentialed) download links.
var fixturePreset = preset{
	id:         "morethantv",
	name:       "MoreThanTV",
	baseURL:    "https://www.morethantv.me/",
	apiPath:    "/api/torznab",
	keyPolicy:  keyRequired,
	keyInfoURL: "https://www.morethantv.me/user/security",
	categories: []int{5030, 5040, 5045, 5060, 2030, 2040, 2045, 2050},
	// The real capture's <link>/enclosure both embed authkey+torrent_pass.
	needsResolver: true,
}

// The driver resolves preset facts (fixed apiPath, key policy, sealing) from the
// package-level table by definition id, so the fixture preset is registered there —
// in this test binary only — to keep exercising the preset-profile path the live
// table no longer covers with its shape.
func init() { presets = append(presets, fixturePreset) }

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
