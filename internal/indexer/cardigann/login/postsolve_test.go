package login

import (
	"context"
	"io"
	stdhttp "net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/loader"
)

// TestFlareSolverrSolvePost asserts SolvePost issues a request.post with the form
// body and returns the resulting cookies + UA.
func TestFlareSolverrSolvePost(t *testing.T) {
	t.Parallel()
	srv, captured := flareStub(t, flareResponse{
		Status: "ok",
		Solution: flareSolution{
			UserAgent: "BrowserUA/1.0",
			Cookies:   []flareCookie{{Name: "uid", Value: "12345"}, {Name: "pass", Value: "abc"}},
		},
	})
	res, err := NewFlareSolverrSolver(srv.URL, 0).SolvePost(context.Background(), "https://t.test/index.php?page=login", "uid=u&pwd=p")
	if err != nil {
		t.Fatalf("SolvePost: %v", err)
	}
	if captured.Cmd != "request.post" || captured.URL != "https://t.test/index.php?page=login" {
		t.Errorf("request = %+v, want cmd=request.post url=login", captured)
	}
	if captured.PostData != "uid=u&pwd=p" {
		t.Errorf("postData = %q, want the form body", captured.PostData)
	}
	if captured.MaxTimeout <= 0 {
		t.Errorf("maxTimeout = %d, want >0", captured.MaxTimeout)
	}
	if res.UserAgent != "BrowserUA/1.0" || len(res.Cookies) != 2 {
		t.Errorf("result = %+v, want UA + 2 auth cookies", res)
	}
}

// postChallengeDoer returns a Cloudflare challenge for every request, simulating a
// tracker whose login POST is guarded by an anti-bot rule harbrr's own client
// cannot clear.
type postChallengeDoer struct{ calls int }

func (d *postChallengeDoer) Do(req *stdhttp.Request) (*stdhttp.Response, error) {
	d.calls++
	return &stdhttp.Response{
		StatusCode: stdhttp.StatusForbidden,
		Header:     stdhttp.Header{},
		Body:       io.NopCloser(strings.NewReader("<html><head><title>Just a moment...</title></head></html>")),
		Request:    req,
	}, nil
}

// TestPostForm_ChallengedLoginRoutesThroughPostSolver is the core gate for the fix:
// when the login POST is blocked by an anti-bot challenge and a POST-capable solver
// is configured, postForm replays the submission through the solver and seeds the
// authenticated cookies + bound UA (so the search stage logs in).
func TestPostForm_ChallengedLoginRoutesThroughPostSolver(t *testing.T) {
	t.Parallel()
	srv, captured := flareStub(t, flareResponse{
		Status: "ok",
		Solution: flareSolution{
			UserAgent: "BrowserUA/1.0",
			Cookies: []flareCookie{
				{Name: "uid", Value: "12345"}, {Name: "pass", Value: "abcdef"}, {Name: "cf_clearance", Value: "CLR"},
			},
		},
	})
	doer := &postChallengeDoer{}
	e := New(WithClient(doer), WithBaseURL("https://t.invalid/"), WithSolver(NewFlareSolverrSolver(srv.URL, 0)))
	def := &loader.Definition{Login: &loader.Login{Path: "index.php?page=login", Method: "post"}}

	err := e.postForm(context.Background(), def, "index.php?page=login",
		url.Values{"uid": {"u"}, "pwd": {"p"}, "logout": {""}})
	if err != nil {
		t.Fatalf("postForm: %v", err)
	}

	// The solver was asked to POST the encoded login form.
	if captured.Cmd != "request.post" {
		t.Errorf("solver cmd = %q, want request.post", captured.Cmd)
	}
	if !strings.Contains(captured.PostData, "uid=u") || !strings.Contains(captured.PostData, "pwd=p") {
		t.Errorf("solver postData = %q, want the encoded login form", captured.PostData)
	}
	// The authenticated session cookies + bound UA are seeded for the host.
	u, _ := url.Parse("https://t.invalid/index.php")
	got := map[string]string{}
	for _, c := range e.Jar.Cookies(u) {
		got[c.Name] = c.Value
	}
	if got["uid"] != "12345" || got["pass"] != "abcdef" {
		t.Errorf("jar after solve = %v, want uid+pass auth cookies seeded", got)
	}
	if e.SolverUserAgent != "BrowserUA/1.0" {
		t.Errorf("SolverUserAgent = %q, want the solver UA persisted", e.SolverUserAgent)
	}
}

// TestPostForm_ChallengedLoginWithoutPostSolver confirms a non-POST-capable solver
// (the default NoopSolver) does NOT route the POST — postForm falls through to the
// error selectors unchanged, preserving existing behaviour.
func TestPostForm_ChallengedLoginWithoutPostSolver(t *testing.T) {
	t.Parallel()
	doer := &postChallengeDoer{}
	e := New(WithClient(doer), WithBaseURL("https://t.invalid/")) // default NoopSolver (not a PostSolver)
	def := &loader.Definition{Login: &loader.Login{Path: "index.php?page=login", Method: "post"}}

	// No error selector declared, so checkErrors returns nil on the challenge body:
	// the point is that postForm does NOT panic/route and issues exactly one request.
	if err := e.postForm(context.Background(), def, "index.php?page=login", url.Values{"uid": {"u"}}); err != nil {
		t.Fatalf("postForm: %v", err)
	}
	if doer.calls != 1 {
		t.Errorf("doer calls = %d, want 1 (no post-solve replay without a PostSolver)", doer.calls)
	}
}
