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

// seqDoer serves a scripted sequence of bodies (the last one repeats) and counts
// calls, so a test can simulate "challenge first, clean on retry".
type seqDoer struct {
	bodies []string
	mu     sync.Mutex
	calls  int
}

func (d *seqDoer) Do(req *stdhttp.Request) (*stdhttp.Response, error) {
	d.mu.Lock()
	i := d.calls
	if i >= len(d.bodies) {
		i = len(d.bodies) - 1
	}
	d.calls++
	body := d.bodies[i]
	d.mu.Unlock()
	return &stdhttp.Response{
		StatusCode: stdhttp.StatusOK,
		Header:     stdhttp.Header{},
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    req,
	}, nil
}

func (d *seqDoer) count() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.calls
}

func TestNoopSolver(t *testing.T) {
	t.Parallel()
	if _, err := (NoopSolver{}).Solve(context.Background(), "https://x.test/"); !errors.Is(err, ErrNoSolverConfigured) {
		t.Errorf("NoopSolver.Solve err = %v, want ErrNoSolverConfigured", err)
	}
}

func TestManualCookieSolver(t *testing.T) {
	t.Parallel()
	res, err := ManualCookieSolver{Cookie: "cf_clearance=abc; sess=42"}.Solve(context.Background(), "https://x.test/")
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}
	if len(res.Cookies) != 2 {
		t.Fatalf("cookies = %d, want 2", len(res.Cookies))
	}
	got := map[string]string{}
	for _, c := range res.Cookies {
		got[c.Name] = c.Value
	}
	if got["cf_clearance"] != "abc" || got["sess"] != "42" {
		t.Errorf("parsed cookies = %v, want cf_clearance=abc sess=42", got)
	}

	if _, err := (ManualCookieSolver{Cookie: "   "}).Solve(context.Background(), "x"); !errors.Is(err, ErrNoSolverConfigured) {
		t.Errorf("blank cookie err = %v, want ErrNoSolverConfigured", err)
	}
}

func TestFetchLandingPastAntiBot_ManualCookie(t *testing.T) {
	t.Parallel()
	doer := &seqDoer{bodies: []string{"Just a moment...", "<html><body>login form</body></html>"}}
	e := New(
		WithClient(doer),
		WithBaseURL("https://t.invalid/"),
		WithSolver(ManualCookieSolver{Cookie: "cf_clearance=token123"}),
	)

	body, err := e.fetchLandingPastAntiBot("https://t.invalid/login.php", nil)
	if err != nil {
		t.Fatalf("fetchLandingPastAntiBot: %v", err)
	}
	if !strings.Contains(string(body), "login form") {
		t.Errorf("body after solve = %q, want the clean login page", body)
	}
	if doer.count() != 2 {
		t.Errorf("doer calls = %d, want 2 (challenge + solved retry)", doer.count())
	}
	// The solved cookie was seeded into the jar for the tracker host.
	u, _ := url.Parse("https://t.invalid/login.php")
	var seeded bool
	for _, c := range e.Jar.Cookies(u) {
		if c.Name == "cf_clearance" && c.Value == "token123" {
			seeded = true
		}
	}
	if !seeded {
		t.Error("solved cf_clearance cookie was not seeded into the jar")
	}
}

func TestFetchLandingPastAntiBot_NoopFailsLoud(t *testing.T) {
	t.Parallel()
	doer := &seqDoer{bodies: []string{"Just a moment..."}}
	e := New(WithClient(doer), WithBaseURL("https://t.invalid/")) // default NoopSolver

	_, err := e.fetchLandingPastAntiBot("https://t.invalid/login.php", nil)
	if !errors.Is(err, ErrSolverRequired) {
		t.Errorf("err = %v, want ErrSolverRequired (no solver configured)", err)
	}
	if doer.count() != 1 {
		t.Errorf("doer calls = %d, want 1 (no retry without a solver)", doer.count())
	}
}

func TestFetchLandingPastAntiBot_CleanPage(t *testing.T) {
	t.Parallel()
	doer := &seqDoer{bodies: []string{"<html><body>login form</body></html>"}}
	e := New(WithClient(doer), WithBaseURL("https://t.invalid/"))

	body, err := e.fetchLandingPastAntiBot("https://t.invalid/login.php", nil)
	if err != nil {
		t.Fatalf("fetchLandingPastAntiBot: %v", err)
	}
	if !strings.Contains(string(body), "login form") {
		t.Errorf("body = %q", body)
	}
	if doer.count() != 1 {
		t.Errorf("doer calls = %d, want 1 (no anti-bot, no solver call)", doer.count())
	}
}
