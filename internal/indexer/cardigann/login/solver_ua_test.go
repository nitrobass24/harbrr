package login

import (
	"context"
	"errors"
	"io"
	stdhttp "net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
)

// uaSolver is a stub solver that returns a fixed User-Agent and cookies, so a
// test can assert the executor persists the UA and seeds the cookies.
type uaSolver struct {
	ua      string
	cookies []*stdhttp.Cookie
}

func (s uaSolver) Solve(context.Context, string) (SolveResult, error) {
	return SolveResult{Cookies: s.cookies, UserAgent: s.ua}, nil
}

// uaRecordingDoer records the User-Agent header of every request it serves, so a
// test can assert the executor replays the solver UA on subsequent requests.
type uaRecordingDoer struct {
	body string
	mu   sync.Mutex
	uas  []string
}

func (d *uaRecordingDoer) Do(req *stdhttp.Request) (*stdhttp.Response, error) {
	d.mu.Lock()
	d.uas = append(d.uas, req.Header.Get("User-Agent"))
	d.mu.Unlock()
	return &stdhttp.Response{
		StatusCode: stdhttp.StatusOK,
		Header:     stdhttp.Header{},
		Body:       io.NopCloser(strings.NewReader(d.body)),
		Request:    req,
	}, nil
}

func (d *uaRecordingDoer) seen() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]string(nil), d.uas...)
}

func TestSolveHost_SeedsCookiesAndPersistsUA(t *testing.T) {
	t.Parallel()
	e := New(
		WithClient(&uaRecordingDoer{}),
		WithBaseURL("https://t.invalid/"),
		WithSolver(uaSolver{
			ua:      "Mozilla/5.0 (solver)",
			cookies: []*stdhttp.Cookie{{Name: "cf_clearance", Value: "CFTOKEN"}},
		}),
	)

	if err := e.SolveHost(t.Context(), "https://t.invalid/"); err != nil {
		t.Fatalf("SolveHost: %v", err)
	}
	if e.SolverUserAgent != "Mozilla/5.0 (solver)" {
		t.Errorf("SolverUserAgent = %q, want the solver UA", e.SolverUserAgent)
	}
	if e.Session().UserAgent != "Mozilla/5.0 (solver)" {
		t.Errorf("Session().UserAgent = %q, want the solver UA", e.Session().UserAgent)
	}
	// cf_clearance seeded into the jar for the host.
	u, _ := url.Parse("https://t.invalid/")
	var seeded bool
	for _, c := range e.Jar.Cookies(u) {
		if c.Name == "cf_clearance" && c.Value == "CFTOKEN" {
			seeded = true
		}
	}
	if !seeded {
		t.Error("cf_clearance was not seeded into the jar by SolveHost")
	}
}

func TestSolveHost_NoSolverDeclines(t *testing.T) {
	t.Parallel()
	e := New(WithClient(&uaRecordingDoer{}), WithBaseURL("https://t.invalid/")) // default NoopSolver
	if err := e.SolveHost(t.Context(), "https://t.invalid/"); !errors.Is(err, ErrNoSolverConfigured) {
		t.Errorf("SolveHost err = %v, want ErrNoSolverConfigured", err)
	}
	if e.SolverUserAgent != "" {
		t.Errorf("SolverUserAgent = %q, want empty when no solve occurred", e.SolverUserAgent)
	}
}

// TestSolverUserAgent_ReplayedOnLoginRequests proves the persisted solver UA is
// sent on every subsequent login-stage request (a UA-bound cf_clearance demands
// it), while a definition's own User-Agent header still wins.
func TestSolverUserAgent_ReplayedOnLoginRequests(t *testing.T) {
	t.Parallel()
	d := &uaRecordingDoer{body: "<html>ok</html>"}
	e := New(WithClient(d), WithBaseURL("https://t.invalid/"))
	e.SolverUserAgent = "Mozilla/5.0 (solver)"

	if _, _, err := e.get(t.Context(), "https://t.invalid/login", nil); err != nil {
		t.Fatalf("get (no def UA): %v", err)
	}
	if _, _, err := e.get(t.Context(), "https://t.invalid/login", map[string][]string{"User-Agent": {"DefUA"}}); err != nil {
		t.Fatalf("get (def UA): %v", err)
	}

	seen := d.seen()
	if len(seen) != 2 {
		t.Fatalf("requests = %d, want 2", len(seen))
	}
	if seen[0] != "Mozilla/5.0 (solver)" {
		t.Errorf("first request UA = %q, want the persisted solver UA", seen[0])
	}
	if seen[1] != "DefUA" {
		t.Errorf("second request UA = %q, want the definition's own UA to win", seen[1])
	}
}
