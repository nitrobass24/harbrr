package search

import (
	"errors"
	stdhttp "net/http"
	"strings"

	apphttp "github.com/autobrr/harbrr/internal/http"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/loader"
)

// maxCaptureBodyBytes caps how much of a failed response a capture retains. The
// executor already buffers up to maxSearchBodyBytes (32 MiB) for parsing; a
// diagnostic snapshot only needs enough of the page to see what the tracker
// actually served, and the ring is held in memory for the process's lifetime.
const maxCaptureBodyBytes = 64 << 10

// CaptureReadLimit is how many response bytes a caller OUTSIDE this package should
// read before handing them to NewCapture: one past the retained cap, so a body that
// exactly fills the cap is still distinguishable from one that was cut.
const CaptureReadLimit = maxCaptureBodyBytes + 1

// Selector-miss kinds. MissNoRows and MissFields are the two the issue exists to
// separate: a rows selector that matched nothing is markup drift at the page
// level, a field that missed inside a matched row is drift at the column level.
// An empty kind means the failure happened before either could be attributed
// (e.g. the document itself would not parse).
const (
	MissNoRows = "no_rows"
	MissFields = "fields"
)

// SelectorMiss attributes a parse failure to the definition node that missed.
// Path is the definition's JSON-pointer location ("/search/rows",
// "/search/fields/title"), Selector the expression that node declared.
type SelectorMiss struct {
	Kind     string
	Selector string
	Path     string
}

// Capture is a redacted snapshot of the tracker exchange that failed, retained
// so an operator can see what the tracker actually served instead of re-running
// the request by hand. It is built from bytes ALREADY fetched — constructing one
// never issues an HTTP request.
//
// Every field is redacted at construction: URL through apphttp.RedactURL,
// credential headers kept by NAME with their value replaced by a placeholder, and
// body/header text run through the definition's configured secret values and the
// shared credential-name scrub. A consumer may surface a Capture as-is.
type Capture struct {
	Method string
	URL    string
	// Status is 0 when the request never produced a response (a transport
	// failure), which is also when Headers and Body are empty.
	Status  int
	Headers map[string]string
	Body    string
	// BodyTruncated reports that Body was cut at maxCaptureBodyBytes.
	BodyTruncated bool
	Miss          SelectorMiss
}

// CaptureError carries a Capture alongside the classified failure it describes.
// It wraps the original error unchanged, so errors.Is/As against ErrParseError,
// RateLimitedError, and every other sentinel behaves exactly as before.
type CaptureError struct {
	Capture Capture
	// Err is the classified failure this capture describes.
	Err error
}

func (e *CaptureError) Error() string { return e.Err.Error() }
func (e *CaptureError) Unwrap() error { return e.Err }

// CaptureOf extracts the diagnostic capture from an error chain, reporting
// ok=false when the failure carries none (a native driver's search path, or a
// failure raised before any request was built).
func CaptureOf(err error) (Capture, bool) {
	if ce, ok := errors.AsType[*CaptureError](err); ok {
		return ce.Capture, true
	}
	return Capture{}, false
}

// selectorMissError threads a selector attribution up from the parse internals
// to the capture site. It is unexported: callers read the attribution off the
// Capture, never off the error.
type selectorMissError struct {
	miss SelectorMiss
	err  error
}

func (e *selectorMissError) Error() string { return e.err.Error() }
func (e *selectorMissError) Unwrap() error { return e.err }

// withMiss attributes err to a definition node, leaving an already-attributed
// error alone so the FIRST (innermost, most specific) attribution wins.
func withMiss(err error, miss SelectorMiss) error {
	var existing *selectorMissError
	if err == nil || errors.As(err, &existing) {
		return err
	}
	return &selectorMissError{miss: miss, err: err}
}

// missOf reads the attribution off an error chain; the zero value means the
// failure could not be pinned to a rows or field selector.
func missOf(err error) SelectorMiss {
	if sme, ok := errors.AsType[*selectorMissError](err); ok {
		return sme.miss
	}
	return SelectorMiss{}
}

// captureRequest wraps a failure that never yielded a response body — a
// transport error, or a status the executor refuses — with a request-summary-only
// capture. The URL is redacted; nothing else about the request is retained.
func captureRequest(br builtRequest, err error) error {
	if err == nil {
		return err
	}
	if _, already := CaptureOf(err); already {
		return err
	}
	return &CaptureError{
		Capture: Capture{Method: br.method, URL: apphttp.RedactURL(br.url)},
		Err:     err,
	}
}

// captureResponse wraps a parse failure with the full redacted exchange: the
// request summary, the response status and headers, the (capped) body, and the
// selector attribution carried up from the parse internals.
func captureResponse(def *loader.Definition, deps Deps, br builtRequest, sr searchResponse, err error) error {
	capture := NewCapture(br.method, br.url, sr.status, sr.header, sr.body,
		loader.SecretValues(def.Settings, deps.Config))
	capture.Miss = missOf(err)
	return &CaptureError{Capture: capture, Err: err}
}

// NewCapture builds the redacted exchange snapshot from bytes ALREADY fetched —
// constructing one never issues a request. secrets are the caller's configured
// credential VALUES; the shared credential-NAME vocabulary is applied on top
// regardless, so a caller with none still gets a scrubbed capture. It is exported so
// the native drivers' Base can capture a refused grab (autobrr/harbrr#465) through
// this same redaction rather than growing a second, drifting scrub.
func NewCapture(method, rawURL string, status int, header stdhttp.Header, body []byte, secrets []string) Capture {
	redacted, truncated := redactBody(body, secrets)
	return Capture{
		Method:        method,
		URL:           apphttp.RedactURL(rawURL),
		Status:        status,
		Headers:       redactHeaders(header, secrets),
		Body:          redacted,
		BodyTruncated: truncated,
	}
}

// redactBody caps the body at maxCaptureBodyBytes and scrubs it twice: first the
// definition's own configured credential VALUES (a page that echoes the submitted
// passkey), then the shared credential-NAME vocabulary (an apikey the tracker
// itself put in the markup). Truncating first keeps the regex passes off a 32 MiB
// body.
//
// Truncating does NOT merely remove secrets, it can SPLIT one: ScrubValues matches
// a configured value in full, so a passkey straddling the cut would leave its
// unmatched prefix behind. Any such prefix begins within the last
// (longest secret - 1) bytes of the cut, so that window is dropped too — after
// which every surviving occurrence is whole and scrubbable.
func redactBody(raw []byte, secrets []string) (string, bool) {
	truncated := len(raw) > maxCaptureBodyBytes
	if truncated {
		raw = raw[:maxCaptureBodyBytes-min(longestLen(secrets), maxCaptureBodyBytes)]
	}
	out := apphttp.RedactSecretsInText(apphttp.ScrubValues(string(raw), secrets))
	if len(out) > maxCaptureBodyBytes {
		out, truncated = out[:maxCaptureBodyBytes], true
	}
	return out, truncated
}

// longestLen is the straddle window: one less than the longest value, and 0 when
// there are no configured secrets (nothing can straddle, so nothing is dropped).
func longestLen(secrets []string) int {
	n := 0
	for _, s := range secrets {
		n = max(n, len(s))
	}
	if n == 0 {
		return 0
	}
	return n - 1
}

// droppedHeaders are response headers whose VALUE is a credential in its
// entirety, so no partial scrub applies — they are replaced wholesale.
var droppedHeaders = map[string]struct{}{
	"authorization":       {},
	"proxy-authorization": {},
	"cookie":              {},
	"set-cookie":          {},
	"www-authenticate":    {},
	"proxy-authenticate":  {},
}

// redactHeaders renders the response headers safe to retain: a credential header
// keeps its NAME (which header was present is itself the diagnostic) but its whole
// value becomes a placeholder, a URL-valued header (Location, Refresh) goes through
// RedactURL, and every other value is run through the configured secret values and
// the shared credential-name scrub.
func redactHeaders(h stdhttp.Header, secrets []string) map[string]string {
	if len(h) == 0 {
		return nil
	}
	out := make(map[string]string, len(h))
	for name, values := range h {
		if _, drop := droppedHeaders[strings.ToLower(name)]; drop {
			out[name] = "REDACTED"
			continue
		}
		v := strings.Join(values, ", ")
		if strings.HasPrefix(v, "http://") || strings.HasPrefix(v, "https://") {
			v = apphttp.RedactURL(v)
		}
		out[name] = apphttp.RedactSecretsInText(apphttp.ScrubValues(v, secrets))
	}
	return out
}
