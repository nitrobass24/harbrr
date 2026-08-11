package search

import (
	"errors"
	"io"
	stdhttp "net/http"
	"strings"
	"testing"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/internal/selector"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/loader"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/normalizer"
)

// capturePasskey is a SYNTHETIC 40-hex credential (test fixture only, built by
// concatenation so secret scanners do not flag it). It rides the request in three
// places a real definition puts one — a path segment, a query param, and the
// response body the tracker echoes — so the capture has to scrub all three.
var capturePasskey = "0f1e2d3c4b5a6978" + "8796a5b4c3d2e1f0" + "0f1e2d3c"

// captureDef is a JSON-path definition whose URL carries the passkey in BOTH a
// path segment and a query param, and whose rows/fields are real. The path
// template renders {{ .Config.passkey }} so the fixture and the redaction target
// are the same value.
const captureDef = `---
id: capfix
name: Capture Fixture
description: runtime parse-failure capture
type: private
language: en-US
encoding: UTF-8
links:
  - https://cap.invalid/
settings:
  - name: passkey
    type: text
    label: Passkey
caps:
  categorymappings:
    - {id: 1, cat: Movies}
  modes:
    search: [q]
search:
  paths:
    - path: /api/{{ .Config.passkey }}
      inheritinputs: true
      response:
        type: json
  inputs:
    q: "{{ .Keywords }}"
    passkey: "{{ .Config.passkey }}"
  rows:
    selector: results
  fields:
    category:
      text: Movies
    title:
      selector: title
    download:
      selector: link
    seeders:
      selector: seeders
    size:
      selector: size
`

// captureDoer serves one scripted response and COUNTS every request, so a test
// can assert the capture path issues none of its own.
type captureDoer struct {
	calls  int
	status int
	header stdhttp.Header
	body   string
	err    error
}

func (d *captureDoer) Do(req *stdhttp.Request) (*stdhttp.Response, error) {
	d.calls++
	if d.err != nil {
		return nil, d.err
	}
	status := d.status
	if status == 0 {
		status = stdhttp.StatusOK
	}
	header := d.header
	if header == nil {
		header = stdhttp.Header{}
	}
	return &stdhttp.Response{
		StatusCode: status,
		Header:     header,
		Body:       io.NopCloser(strings.NewReader(d.body)),
		Request:    req,
	}, nil
}

func captureDeps() Deps {
	return Deps{
		Filters:    NewFilterRegistry(stubDateParse, stubRelTime, ""),
		Normalizer: &normalizer.Normalizer{BaseURL: "https://cap.invalid/"},
		BaseURL:    "https://cap.invalid/",
		Config:     map[string]string{"passkey": capturePasskey},
	}
}

// runCapture drives Execute against a one-shot doer and returns the capture the
// failure carried, failing the test when there is none.
func runCapture(t *testing.T, doer *captureDoer) Capture {
	t.Helper()
	def, err := loader.Parse([]byte(captureDef))
	if err != nil {
		t.Fatalf("loader.Parse: %v", err)
	}
	_, err = Execute(t.Context(), def, Query{Keywords: "ubuntu"}, nil, doer, selector.New(), captureDeps())
	if err == nil {
		t.Fatal("Execute succeeded, want a classified failure")
	}
	capture, ok := CaptureOf(err)
	if !ok {
		t.Fatalf("error carries no capture: %v", err)
	}
	return capture
}

// TestCapture_IssuesNoAdditionalRequest is the constraint the whole feature rests
// on: retaining the failed response reuses bytes already in hand, so the request
// count through the doer is exactly the one search request — never a re-fetch.
func TestCapture_IssuesNoAdditionalRequest(t *testing.T) {
	t.Parallel()

	doer := &captureDoer{body: `{"nope":[]}`}
	capture := runCapture(t, doer)

	if doer.calls != 1 {
		t.Errorf("tracker requests = %d, want exactly 1 (the search itself)", doer.calls)
	}
	if capture.Status != stdhttp.StatusOK {
		t.Errorf("capture status = %d, want 200", capture.Status)
	}
}

// TestCapture_SuccessfulSearchRetainsNothing: a page that parses produces no
// error, so there is nothing to extract a capture from.
func TestCapture_SuccessfulSearchRetainsNothing(t *testing.T) {
	t.Parallel()

	doer := &captureDoer{body: `{"results":[{"title":"Ubuntu","link":"/dl/1","seeders":"5","size":"1 GB"}]}`}
	def, err := loader.Parse([]byte(captureDef))
	if err != nil {
		t.Fatalf("loader.Parse: %v", err)
	}
	rels, err := Execute(t.Context(), def, Query{Keywords: "ubuntu"}, nil, doer, selector.New(), captureDeps())
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(rels) != 1 {
		t.Fatalf("releases = %d, want 1", len(rels))
	}
	if _, ok := CaptureOf(err); ok {
		t.Error("a successful search produced a capture")
	}
}

// TestCapture_Redaction is the non-negotiable one: a failing response whose URL,
// headers, and body all embed credentials must yield a capture containing none of
// them — the passkey in the query, the passkey in the PATH, the Cookie/Set-Cookie
// headers, and a credential-shaped key in the body the tracker itself served.
func TestCapture_Redaction(t *testing.T) {
	t.Parallel()

	sessionCookie := "session=" + "s3cr3tsessionvalue"
	bodyAPIKey := "b4dc0ffee" + "b4dc0ffee"
	doer := &captureDoer{
		header: stdhttp.Header{
			"Set-Cookie":   []string{sessionCookie},
			"Cookie":       []string{sessionCookie},
			"Content-Type": []string{"application/json"},
			"Location":     []string{"https://cap.invalid/login?passkey=" + capturePasskey},
		},
		// Valid JSON with no rows array: the parse fails AFTER the body is in hand,
		// which is exactly the case the capture exists for. The body echoes both the
		// submitted passkey and a credential-NAMED key of the tracker's own.
		body: `{"error":"bad","apikey":"` + bodyAPIKey + `","echo":"` + capturePasskey + `"}`,
	}
	capture := runCapture(t, doer)

	parts := make([]string, 0, 3+len(capture.Headers))
	parts = append(parts, capture.Method, capture.URL, capture.Body)
	for name, value := range capture.Headers {
		parts = append(parts, name+": "+value)
	}
	rendered := strings.Join(parts, " ")
	for _, secret := range []string{capturePasskey, sessionCookie, bodyAPIKey, "s3cr3tsessionvalue"} {
		if strings.Contains(rendered, secret) {
			t.Errorf("capture leaked %q:\n%s", secret, rendered)
		}
	}
	// Positive side: the placeholders prove each secret was PRESENT and masked,
	// not merely absent because the fixture never carried it.
	if !strings.Contains(capture.URL, "/api/REDACTED?passkey=REDACTED") {
		t.Errorf("URL path token and query param not both masked: %q", capture.URL)
	}
	if !strings.Contains(capture.Body, `"apikey":"<redacted>"`) {
		t.Errorf("body apikey not masked: %q", capture.Body)
	}
	if !strings.Contains(capture.Body, "[redacted]") {
		t.Errorf("echoed passkey value not scrubbed: %q", capture.Body)
	}
	// The scrub must not gut the diagnostic value: the non-secret context survives.
	if !strings.Contains(capture.URL, "cap.invalid") {
		t.Errorf("capture URL lost its host: %q", capture.URL)
	}
	if !strings.Contains(capture.Body, `"error"`) {
		t.Errorf("capture body lost its non-secret content: %q", capture.Body)
	}
	if got := capture.Headers["Content-Type"]; got != "application/json" {
		t.Errorf("Content-Type = %q, want it preserved", got)
	}
	if got := capture.Headers["Set-Cookie"]; got != "REDACTED" {
		t.Errorf("Set-Cookie = %q, want REDACTED", got)
	}
}

// TestCapture_BodyCap holds the 64 KiB ceiling: an oversized failing body is cut
// and says so, so a broken tracker cannot pin an unbounded page in memory.
func TestCapture_BodyCap(t *testing.T) {
	t.Parallel()

	doer := &captureDoer{body: `{"junk":"` + strings.Repeat("x", 200<<10) + `"}`}
	capture := runCapture(t, doer)

	if len(capture.Body) > maxCaptureBodyBytes {
		t.Errorf("captured body = %d bytes, want <= %d", len(capture.Body), maxCaptureBodyBytes)
	}
	if !capture.BodyTruncated {
		t.Error("BodyTruncated = false, want true for an oversized body")
	}
}

// TestCapture_BodyCapDoesNotSplitASecret is the nasty edge of the cap: truncation
// does not only DROP bytes, it can SPLIT a configured credential across the cut,
// leaving a prefix that ScrubValues (which matches the value in full) no longer
// recognizes. The passkey here straddles the 64 KiB boundary exactly.
func TestCapture_BodyCapDoesNotSplitASecret(t *testing.T) {
	t.Parallel()

	// Place the passkey so 20 of its 40 bytes fall inside the cap and 20 outside.
	pad := strings.Repeat("x", maxCaptureBodyBytes-20)
	doer := &captureDoer{body: pad + capturePasskey + strings.Repeat("y", 1024)}
	capture := runCapture(t, doer)

	if !capture.BodyTruncated {
		t.Error("BodyTruncated = false, want true for an oversized body")
	}
	// No fragment of the credential survives: every 8-byte window of it must be
	// absent, which catches a straddling prefix as well as the whole value.
	for i := 0; i+8 <= len(capturePasskey); i++ {
		if frag := capturePasskey[i : i+8]; strings.Contains(capture.Body, frag) {
			t.Fatalf("captured body retained credential fragment %q at offset %d", frag, i)
		}
	}
}

// TestCapture_SelectorMiss is the acceptance point the issue names: "no rows
// matched" and "rows matched but the fields didn't" are DIFFERENT problems and
// must be reported as such, each naming the definition node that missed.
func TestCapture_SelectorMiss(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		body         string
		wantKind     string
		wantSelector string
		wantPath     string
	}{
		{
			// The rows array the definition names is simply not there.
			name:     "no rows matched",
			body:     `{"other":[{"title":"Ubuntu"}]}`,
			wantKind: MissNoRows, wantSelector: "results", wantPath: "/search/rows",
		},
		{
			// A row IS there; the first required field inside it is not.
			name:     "rows matched but fields did not",
			body:     `{"results":[{"nottitle":"Ubuntu"}]}`,
			wantKind: MissFields, wantSelector: "title", wantPath: "/search/fields/title",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			capture := runCapture(t, &captureDoer{body: tt.body})
			if capture.Miss.Kind != tt.wantKind {
				t.Errorf("miss kind = %q, want %q", capture.Miss.Kind, tt.wantKind)
			}
			if capture.Miss.Selector != tt.wantSelector {
				t.Errorf("miss selector = %q, want %q", capture.Miss.Selector, tt.wantSelector)
			}
			if capture.Miss.Path != tt.wantPath {
				t.Errorf("miss path = %q, want %q", capture.Miss.Path, tt.wantPath)
			}
		})
	}
}

// TestCapture_TransportFailureKeepsRequestSummary: with no response in hand the
// capture still names the (redacted) request, and the original error still
// classifies — the capture wrapper is transparent to errors.Is.
func TestCapture_TransportFailureKeepsRequestSummary(t *testing.T) {
	t.Parallel()

	boom := errors.New("dial tcp: connection refused")
	capture := runCapture(t, &captureDoer{err: boom})

	if capture.Status != 0 || capture.Body != "" || len(capture.Headers) != 0 {
		t.Errorf("transport capture carried response state: %+v", capture)
	}
	if capture.Method != stdhttp.MethodGet || !strings.Contains(capture.URL, "cap.invalid") {
		t.Errorf("transport capture lost the request summary: %+v", capture)
	}
	if strings.Contains(capture.URL, capturePasskey) {
		t.Errorf("transport capture leaked the passkey: %q", capture.URL)
	}
}
