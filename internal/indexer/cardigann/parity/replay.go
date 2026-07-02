package parity

import (
	"bytes"
	"fmt"
	"io"
	stdhttp "net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	apphttp "github.com/autobrr/harbrr/internal/http"
)

// replay is the offline transport that backs search-mode cases. It serves a
// fixed, ordered sequence of saved responses and asserts the engine issued
// exactly the requests the case declares — method and full URL — so a case pins
// request construction (the strongest parity signal), not just response parsing.
//
// It is an http.RoundTripper wrapped by runSearch in a real *http.Client with a
// cookie jar, so the production cookie flow (a login response's Set-Cookie
// carried into the search request) is exercised offline, exactly as the login
// package's replay tests do.
type replay struct {
	dir   string
	steps []CaseStep

	mu  sync.Mutex
	idx int
	err error
}

// newReplay builds a replay transport for the case's steps, reading bodies from
// dir on demand.
func newReplay(dir string, steps []CaseStep) *replay {
	return &replay{dir: dir, steps: steps}
}

// RoundTrip asserts the outgoing request matches the next expected step and
// serves that step's canned response. An unexpected or mismatched request is
// recorded as a loud error (surfaced by done) and returned, never silently
// served. URLs in errors are redacted — a search URL can carry a passkey.
func (r *replay) RoundTrip(req *stdhttp.Request) (*stdhttp.Response, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if req.Body != nil {
		_, _ = io.Copy(io.Discard, req.Body)
		_ = req.Body.Close()
	}

	if r.idx >= len(r.steps) {
		return nil, r.fail("unexpected request %s %s (no more steps)", req.Method, apphttp.RedactURL(req.URL.String()))
	}
	step := r.steps[r.idx]
	r.idx++

	if !equalFoldMethod(req.Method, step.Method) || req.URL.String() != step.URL {
		return nil, r.fail("step %d: got %s %s, want %s %s", r.idx-1,
			req.Method, apphttp.RedactURL(req.URL.String()), step.Method, apphttp.RedactURL(step.URL))
	}

	if step.ExpectCookie != "" && !strings.Contains(req.Header.Get("Cookie"), step.ExpectCookie) {
		// The expected cookie value is never echoed — Cookie material stays out of
		// logs/traces (AGENTS.md redaction rule), even for synthetic fixtures.
		return nil, r.fail("step %d: request Cookie header did not contain the expected session cookie (it did not propagate)", r.idx-1)
	}

	resp, err := r.serve(req, step)
	if err != nil {
		return nil, r.fail("step %d: %v", r.idx-1, err)
	}
	return resp, nil
}

// serve builds the canned response for a step from its saved body file.
func (r *replay) serve(req *stdhttp.Request, step CaseStep) (*stdhttp.Response, error) {
	var body []byte
	if step.Response != "" {
		raw, err := os.ReadFile(filepath.Join(r.dir, step.Response)) //nolint:gosec // case-fixture path under testdata/.
		if err != nil {
			return nil, fmt.Errorf("reading step response %q: %w", step.Response, err)
		}
		body = raw
	}
	status := step.Status
	if status == 0 {
		status = stdhttp.StatusOK
	}
	header := stdhttp.Header{}
	for _, c := range step.SetCookie {
		header.Add("Set-Cookie", c)
	}
	return &stdhttp.Response{
		StatusCode: status,
		Status:     strconv.Itoa(status),
		Header:     header,
		Body:       io.NopCloser(bytes.NewReader(body)),
		Request:    req,
	}, nil
}

// done reports the first replay fault (a mismatched/unexpected request) and that
// every declared step was consumed. The engine swallows transport errors into
// its own wrapped error, so the harness consults this for the precise reason.
func (r *replay) done() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.err != nil {
		return r.err
	}
	if r.idx != len(r.steps) {
		return fmt.Errorf("replay: %d of %d steps consumed (the engine issued fewer requests than the case declares)", r.idx, len(r.steps))
	}
	return nil
}

// fail records the first fault and returns it (so RoundTrip can also return it to
// the engine). Caller holds r.mu.
func (r *replay) fail(format string, args ...any) error {
	err := fmt.Errorf("replay: "+format, args...)
	if r.err == nil {
		r.err = err
	}
	return err
}

// equalFoldMethod compares HTTP methods case-insensitively (the engine
// uppercases; cases may write GET/get).
func equalFoldMethod(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		ca, cb := a[i], b[i]
		if ca >= 'a' && ca <= 'z' {
			ca -= 'a' - 'A'
		}
		if cb >= 'a' && cb <= 'z' {
			cb -= 'a' - 'A'
		}
		if ca != cb {
			return false
		}
	}
	return true
}
