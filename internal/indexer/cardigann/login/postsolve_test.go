package login

import (
	"context"
	"io"
	stdhttp "net/http"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/loader"
)

// postThenCleanDoer returns a Cloudflare challenge for the FIRST POST (the
// pre-clearance submission) and the clean logged-in page for the retry, recording
// the retry request's headers so the cf_clearance + UA replay can be asserted.
type postThenCleanDoer struct {
	mu            sync.Mutex
	posts         int
	retryHeaders  stdhttp.Header
	retryHadCFCkz bool
}

func (d *postThenCleanDoer) Do(req *stdhttp.Request) (*stdhttp.Response, error) {
	d.mu.Lock()
	d.posts++
	first := d.posts == 1
	if !first {
		d.retryHeaders = req.Header.Clone()
		for _, c := range req.Cookies() {
			if c.Name == "cf_clearance" {
				d.retryHadCFCkz = true
			}
		}
	}
	d.mu.Unlock()
	body := "<html><body>welcome, logged in</body></html>"
	if first {
		body = "<html><head><title>Just a moment...</title></head></html>"
	}
	return &stdhttp.Response{
		StatusCode: stdhttp.StatusOK,
		Header:     stdhttp.Header{},
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    req,
	}, nil
}

// solveURLSolver is a stub solver that returns a cf_clearance cookie + UA for any
// GET-solve, simulating FlareSolverr clearing the challenged login page.
type solveURLSolver struct{ solved int }

func (s *solveURLSolver) Solve(context.Context, string) (SolveResult, error) {
	s.solved++
	return SolveResult{
		UserAgent: "BrowserUA/1.0",
		Cookies:   []*stdhttp.Cookie{{Name: "cf_clearance", Value: "CLR"}},
	}, nil
}

// TestPostForm_ChallengedLoginGetSolvesThenRetries is the core gate: a login POST
// blocked by an anti-bot challenge triggers a GET-solve of the login URL (yielding
// cf_clearance + UA) and a retry POST that carries them and succeeds.
func TestPostForm_ChallengedLoginGetSolvesThenRetries(t *testing.T) {
	t.Parallel()
	doer := &postThenCleanDoer{}
	solver := &solveURLSolver{}
	e := New(WithClient(doer), WithBaseURL("https://t.invalid/"), WithSolver(solver))
	def := &loader.Definition{Login: &loader.Login{Path: "index.php?page=login", Method: "post"}}

	if err := e.postForm(context.Background(), def, "index.php?page=login",
		url.Values{"uid": {"u"}, "pwd": {"p"}, "logout": {""}}); err != nil {
		t.Fatalf("postForm: %v", err)
	}
	if solver.solved != 1 {
		t.Errorf("solver GET-solves = %d, want 1 (clear the challenged login URL)", solver.solved)
	}
	if doer.posts != 2 {
		t.Errorf("POSTs = %d, want 2 (challenged + retry after clearance)", doer.posts)
	}
	if !doer.retryHadCFCkz {
		t.Error("retry POST did not carry the cf_clearance cookie")
	}
	if ua := doer.retryHeaders.Get("User-Agent"); ua != "BrowserUA/1.0" {
		t.Errorf("retry POST User-Agent = %q, want the solver UA", ua)
	}
	if e.SolverUserAgent != "BrowserUA/1.0" {
		t.Errorf("SolverUserAgent = %q, want the solver UA persisted for search", e.SolverUserAgent)
	}
}

// challengeDoer always returns a Cloudflare challenge, so a retry is still blocked.
type challengeDoer struct{ posts int }

func (d *challengeDoer) Do(req *stdhttp.Request) (*stdhttp.Response, error) {
	d.posts++
	return &stdhttp.Response{
		StatusCode: stdhttp.StatusForbidden,
		Header:     stdhttp.Header{},
		Body:       io.NopCloser(strings.NewReader("<html><head><title>Just a moment...</title></head></html>")),
		Request:    req,
	}, nil
}

// TestPostForm_ChallengedLoginNoSolverFailsLoud confirms that without a solver, a
// challenged login POST fails loud (ErrSolverRequired) rather than silently
// treating the challenge page as a successful login.
func TestPostForm_ChallengedLoginNoSolverFailsLoud(t *testing.T) {
	t.Parallel()
	doer := &challengeDoer{}
	e := New(WithClient(doer), WithBaseURL("https://t.invalid/")) // default NoopSolver
	def := &loader.Definition{Login: &loader.Login{Path: "index.php?page=login", Method: "post"}}

	err := e.postForm(context.Background(), def, "index.php?page=login", url.Values{"uid": {"u"}})
	if err == nil {
		t.Fatal("want an error when the login POST is challenged and no solver is configured")
	}
	if doer.posts != 1 {
		t.Errorf("POSTs = %d, want 1 (no retry without a solver)", doer.posts)
	}
}

// TestPostForm_ChallengedLoginStillChallengedFailsLoud confirms that if the retry
// POST is STILL challenged after solving, it fails loud rather than looping.
func TestPostForm_ChallengedLoginStillChallengedFailsLoud(t *testing.T) {
	t.Parallel()
	doer := &challengeDoer{}
	solver := &solveURLSolver{}
	e := New(WithClient(doer), WithBaseURL("https://t.invalid/"), WithSolver(solver))
	def := &loader.Definition{Login: &loader.Login{Path: "index.php?page=login", Method: "post"}}

	if err := e.postForm(context.Background(), def, "index.php?page=login", url.Values{"uid": {"u"}}); err == nil {
		t.Fatal("want an error when the retry POST is still challenged")
	}
	if doer.posts != 2 {
		t.Errorf("POSTs = %d, want exactly 2 (challenged + one retry, no loop)", doer.posts)
	}
}
