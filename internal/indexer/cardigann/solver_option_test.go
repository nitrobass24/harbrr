package cardigann

import (
	"testing"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/login"
)

// TestSolverOption verifies the config -> solver mapping the registry relies on:
// "manual_cookie" wires a ManualCookieSolver carrying the encrypted cookie;
// "flaresolverr" wires a FlareSolverrSolver from flaresolverr_url; anything else
// (unset/empty) leaves the solver unset so the login executor uses its NoopSolver.
func TestSolverOption(t *testing.T) {
	t.Parallel()

	var o options
	SolverOption(map[string]string{"solver_type": "manual_cookie", "cookie": "cf_clearance=1"})(&o)
	mc, ok := o.solver.(login.ManualCookieSolver)
	if !ok {
		t.Fatalf("solver = %T, want login.ManualCookieSolver", o.solver)
	}
	if mc.Cookie != "cf_clearance=1" {
		t.Errorf("cookie = %q, want cf_clearance=1", mc.Cookie)
	}

	var fs options
	SolverOption(map[string]string{"solver_type": "flaresolverr", "flaresolverr_url": "http://fs:8191"})(&fs)
	if _, ok := fs.solver.(*login.FlareSolverrSolver); !ok {
		t.Fatalf("solver = %T, want *login.FlareSolverrSolver", fs.solver)
	}

	for _, cfg := range []map[string]string{
		{},
		{"solver_type": ""},
		{"solver_type": "unknown"},
	} {
		var got options
		SolverOption(cfg)(&got)
		if got.solver != nil {
			t.Errorf("SolverOption(%v) solver = %v, want nil (default NoopSolver)", cfg, got.solver)
		}
	}
}
