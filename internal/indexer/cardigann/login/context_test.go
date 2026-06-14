package login

import (
	"context"
	"errors"
	"io"
	stdhttp "net/http"
	"strings"
	"testing"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/loader"
)

// ctxProbeKey types the sentinel context value the solver-propagation test places
// on the request ctx, so the solver can prove it received the caller's ctx.
type ctxProbeKey struct{}

// ctxAbortDoer honors context cancellation the way the stdlib *http.Client does,
// so a test can prove the login flow threads the caller's ctx all the way to the
// request (rather than the old hard-coded context.Background() in do()).
type ctxAbortDoer struct{}

func (ctxAbortDoer) Do(req *stdhttp.Request) (*stdhttp.Response, error) {
	if err := req.Context().Err(); err != nil {
		return nil, err
	}
	return &stdhttp.Response{
		StatusCode: stdhttp.StatusOK,
		Header:     stdhttp.Header{},
		Body:       io.NopCloser(strings.NewReader("<html></html>")),
		Request:    req,
	}, nil
}

// TestLogin_ContextCancelledAborts proves login.go's do() threads the caller ctx
// onto the request: a pre-cancelled context aborts the login GET with
// context.Canceled instead of dialing.
func TestLogin_ContextCancelledAborts(t *testing.T) {
	t.Parallel()
	def := &loader.Definition{Login: &loader.Login{Method: "get", Path: "login.php"}}
	e := New(WithClient(ctxAbortDoer{}), WithBaseURL(baseURL), WithConfig(map[string]string{}))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := e.Login(ctx, def); !errors.Is(err, context.Canceled) {
		t.Fatalf("Login err = %v, want context.Canceled", err)
	}
}

// recordingSolver captures the sentinel value carried on the context it is invoked
// with (not the context itself, to satisfy containedctx), so a test can assert the
// anti-bot solver call site receives the threaded request ctx and not a fresh
// context.Background().
type recordingSolver struct{ gotVal string }

func (s *recordingSolver) Solve(ctx context.Context, _ string) (SolveResult, error) {
	if v, ok := ctx.Value(ctxProbeKey{}).(string); ok {
		s.gotVal = v
	}
	return SolveResult{}, nil
}

// TestFetchLandingPastAntiBot_ThreadsContextToSolver proves the solver call site
// (previously a hard-coded context.Background()) receives the caller's ctx: a
// sentinel value placed on the ctx is observable inside Solve.
func TestFetchLandingPastAntiBot_ThreadsContextToSolver(t *testing.T) {
	t.Parallel()
	doer := &seqDoer{bodies: []string{"Just a moment...", "<html><body>login form</body></html>"}}
	sol := &recordingSolver{}
	e := New(WithClient(doer), WithBaseURL("https://t.invalid/"), WithSolver(sol))

	ctx := context.WithValue(context.Background(), ctxProbeKey{}, "sentinel")
	if _, err := e.fetchLandingPastAntiBot(ctx, "https://t.invalid/login.php", nil); err != nil {
		t.Fatalf("fetchLandingPastAntiBot: %v", err)
	}
	if sol.gotVal != "sentinel" {
		t.Fatalf("solver did not receive the threaded ctx; got %q", sol.gotVal)
	}
}
