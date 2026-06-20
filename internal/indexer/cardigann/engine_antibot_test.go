package cardigann

import (
	"context"
	stdhttp "net/http"
	"sync"
	"testing"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/login"
)

const engineSolverUA = "Mozilla/5.0 (antibot-solver)"

// uaStubSolver is a stub anti-bot solver returning a fixed User-Agent and a
// cf_clearance cookie, so the engine test can assert the logged-out search
// recovery clears the host AND replays the bound UA on the relogin + retry.
type uaStubSolver struct {
	mu    sync.Mutex
	calls int
}

func (s *uaStubSolver) Solve(context.Context, string) (login.SolveResult, error) {
	s.mu.Lock()
	s.calls++
	s.mu.Unlock()
	return login.SolveResult{
		UserAgent: engineSolverUA,
		Cookies:   []*stdhttp.Cookie{{Name: "cf_clearance", Value: "CFTOKEN"}},
	}, nil
}

func (s *uaStubSolver) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

// TestSearch_LazyRelogin_SolverClearsHostAndReplaysUA proves the Cloudflare +
// form-login recovery path: a logged-out search response triggers a host anti-bot
// solve (the engine's clearSearchAntiBot) that persists the solver's UA and seeds
// cf_clearance, after which the relogin GET and the retried search both carry that
// UA and the clearance cookie. This is the path a real CF-protected tracker (e.g.
// HD-Space) needs, where the prior login-only solver wiring left search blind.
func TestSearch_LazyRelogin_SolverClearsHostAndReplaysUA(t *testing.T) {
	t.Parallel()
	def := loadFixtureDef(t, "lazy_login.yml")
	doer := &lazyLoginDoer{}
	solver := &uaStubSolver{}
	eng, err := NewEngine(def, WithClock(fixedClock()), WithDoer(doer), WithSolver(solver))
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}

	releases, err := eng.Search(t.Context(), Query{Keywords: "lazy"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(releases) != 1 {
		t.Fatalf("releases = %d, want 1 (retry after host clear + relogin parses results)", len(releases))
	}
	if solver.count() < 1 {
		t.Errorf("solver calls = %d, want >= 1 (logged-out recovery clears the host)", solver.count())
	}

	browse := browseRequests(doer)
	if len(browse) != 2 {
		t.Fatalf("/browse requests = %d, want 2 (logged-out + one retry)", len(browse))
	}
	// First search is pre-solve: no solver UA yet.
	if ua := browse[0].Header.Get("User-Agent"); ua == engineSolverUA {
		t.Errorf("first /browse carried the solver UA %q before any solve", ua)
	}
	// The retried search carries the solver UA and the seeded cf_clearance.
	if ua := browse[1].Header.Get("User-Agent"); ua != engineSolverUA {
		t.Errorf("retried /browse User-Agent = %q, want the solver UA", ua)
	}
	if c, err := browse[1].Cookie("cf_clearance"); err != nil || c.Value != "CFTOKEN" {
		t.Errorf("retried /browse cf_clearance = %v (err %v), want CFTOKEN", c, err)
	}

	// The relogin GET, issued after the host clear, also replays the solver UA.
	login := pathRequests(doer, "/login.php")
	if len(login) != 1 {
		t.Fatalf("/login.php requests = %d, want 1 (single relogin)", len(login))
	}
	if ua := login[0].Header.Get("User-Agent"); ua != engineSolverUA {
		t.Errorf("relogin /login.php User-Agent = %q, want the solver UA", ua)
	}
}

func browseRequests(d *lazyLoginDoer) []*stdhttp.Request { return pathRequests(d, "/browse") }

func pathRequests(d *lazyLoginDoer, path string) []*stdhttp.Request {
	d.mu.Lock()
	defer d.mu.Unlock()
	var out []*stdhttp.Request
	for _, r := range d.requests {
		if r.URL.Path == path {
			out = append(out, r)
		}
	}
	return out
}
