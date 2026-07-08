package login

import (
	"context"
	"errors"
	"io"
	stdhttp "net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/loader"
)

// postThenCleanDoer returns a Cloudflare challenge for the FIRST POST (the
// pre-clearance submission) and the clean logged-in page for the retry, recording
// the retry request's headers so the cf_clearance + UA replay can be asserted.
// It is an http.RoundTripper so a real *http.Client (owning the shared jar, the
// production shape) can wrap it — the solver seeds cf_clearance into that jar
// and the client puts it on the retry's wire.
type postThenCleanDoer struct {
	mu            sync.Mutex
	posts         int
	retryHeaders  stdhttp.Header
	retryHadCFCkz bool
}

func (d *postThenCleanDoer) RoundTrip(req *stdhttp.Request) (*stdhttp.Response, error) {
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
		Cookies:   []*stdhttp.Cookie{{Name: "cf_clearance", Value: "CLR"}}, //nolint:gosec // request cookie; Set-Cookie security attrs are N/A
	}, nil
}

// TestPostForm_ChallengedLoginGetSolvesThenRetries is the core gate: a login POST
// blocked by an anti-bot challenge triggers a GET-solve of the login URL (yielding
// cf_clearance + UA) and a retry POST that carries them and succeeds.
func TestPostForm_ChallengedLoginGetSolvesThenRetries(t *testing.T) {
	t.Parallel()
	doer := &postThenCleanDoer{}
	solver := &solveURLSolver{}
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar.New: %v", err)
	}
	client := &stdhttp.Client{Transport: doer, Jar: jar}
	e := New(WithClient(client), WithJar(jar), WithBaseURL("https://t.invalid/"), WithSolver(solver))
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

// formSubmitDoer serves the clean login form for every GET (the landing page is
// NOT challenged) and a Cloudflare challenge for the first POST; the retry POST
// gets the logged-in page unless failAllPosts is set. It records the retry
// request so the cf_clearance + UA replay can be asserted — postThenCleanDoer's
// counterpart for the form method, whose landing GET must succeed first.
type formSubmitDoer struct {
	mu            sync.Mutex
	failAllPosts  bool
	posts         int
	retryHeaders  stdhttp.Header
	retryHadCFCkz bool
}

const formLandingHTML = `<html><body><form action="/takelogin.php">` +
	`<input type="hidden" name="csrf" value="TOK-123"/></form></body></html>`

func (d *formSubmitDoer) RoundTrip(req *stdhttp.Request) (*stdhttp.Response, error) {
	if req.Method != stdhttp.MethodPost {
		return htmlResponse(req, stdhttp.StatusOK, formLandingHTML), nil
	}
	d.mu.Lock()
	d.posts++
	challenged := d.posts == 1 || d.failAllPosts
	if !challenged {
		d.retryHeaders = req.Header.Clone()
		for _, c := range req.Cookies() {
			if c.Name == "cf_clearance" {
				d.retryHadCFCkz = true
			}
		}
	}
	d.mu.Unlock()
	if challenged {
		return htmlResponse(req, stdhttp.StatusForbidden,
			"<html><head><title>Just a moment...</title></head></html>"), nil
	}
	return htmlResponse(req, stdhttp.StatusOK, "<html><body>welcome, logged in</body></html>"), nil
}

func htmlResponse(req *stdhttp.Request, status int, body string) *stdhttp.Response {
	return &stdhttp.Response{
		StatusCode: status,
		Header:     stdhttp.Header{},
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    req,
	}
}

// formChallengeDef builds a minimal form-method login. Method is deliberately
// left UNSET: Jackett (and Login()) default it to "form", so these tests cover
// the corpus's default login path, not just an explicit `method: form`.
func formChallengeDef() *loader.Definition {
	return &loader.Definition{Login: &loader.Login{
		Path:   "login.php",
		Inputs: map[string]loader.Scalar{"username": scalar("u"), "password": scalar("p")},
	}}
}

// TestLoginForm_ChallengedSubmitGetSolvesThenRetries is the form-method (the
// DEFAULT method) counterpart of TestPostForm_ChallengedLoginGetSolvesThenRetries:
// the landing GET is clean but the submit POST is anti-bot challenged — which
// previously slipped through checkErrors (no 401, no error-selector match) as a
// silent no-cookie "success". The fix GET-solves the submit URL and retries the
// POST carrying cf_clearance + the solver UA.
func TestLoginForm_ChallengedSubmitGetSolvesThenRetries(t *testing.T) {
	t.Parallel()
	doer := &formSubmitDoer{}
	solver := &solveURLSolver{}
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar.New: %v", err)
	}
	client := &stdhttp.Client{Transport: doer, Jar: jar}
	e := New(WithClient(client), WithJar(jar), WithBaseURL("https://t.invalid/"), WithSolver(solver))

	if err := e.Login(context.Background(), formChallengeDef()); err != nil {
		t.Fatalf("Login: %v", err)
	}
	if solver.solved != 1 {
		t.Errorf("solver GET-solves = %d, want 1 (clear the challenged submit URL)", solver.solved)
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

// TestLoginForm_ChallengedSubmitNoSolverFailsLoud pins the U4-F2 regression: a
// challenged form submit with no solver must surface ErrSolverRequired — never
// return nil (the old silent false success with an empty jar) and never
// mis-report ErrLoginFailed.
func TestLoginForm_ChallengedSubmitNoSolverFailsLoud(t *testing.T) {
	t.Parallel()
	doer := &formSubmitDoer{failAllPosts: true}
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar.New: %v", err)
	}
	client := &stdhttp.Client{Transport: doer, Jar: jar}
	e := New(WithClient(client), WithJar(jar), WithBaseURL("https://t.invalid/")) // default NoopSolver

	err = e.Login(context.Background(), formChallengeDef())
	if !errors.Is(err, ErrSolverRequired) {
		t.Fatalf("Login err = %v, want ErrSolverRequired (a challenged submit must fail loud)", err)
	}
	if doer.posts != 1 {
		t.Errorf("POSTs = %d, want 1 (no retry without a solver)", doer.posts)
	}
}

// TestLoginForm_ChallengedSubmitStillChallengedFailsLoud confirms the form path
// inherits solveAndRetryLoginPost's no-loop guarantee: a retry POST that is
// STILL challenged after solving fails loud with ErrSolverRequired.
func TestLoginForm_ChallengedSubmitStillChallengedFailsLoud(t *testing.T) {
	t.Parallel()
	doer := &formSubmitDoer{failAllPosts: true}
	solver := &solveURLSolver{}
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar.New: %v", err)
	}
	client := &stdhttp.Client{Transport: doer, Jar: jar}
	e := New(WithClient(client), WithJar(jar), WithBaseURL("https://t.invalid/"), WithSolver(solver))

	err = e.Login(context.Background(), formChallengeDef())
	if !errors.Is(err, ErrSolverRequired) {
		t.Fatalf("Login err = %v, want ErrSolverRequired", err)
	}
	if doer.posts != 2 {
		t.Errorf("POSTs = %d, want exactly 2 (challenged + one retry, no loop)", doer.posts)
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
