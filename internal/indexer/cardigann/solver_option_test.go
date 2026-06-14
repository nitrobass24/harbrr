package cardigann

import (
	"testing"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/login"
)

// TestSolverOption verifies the config -> solver mapping the registry relies on:
// "manual_cookie" wires a ManualCookieSolver carrying the encrypted cookie;
// anything else (unset, or the Phase-6-deferred "flaresolverr") leaves the solver
// unset so the login executor falls back to its NoopSolver.
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

	for _, cfg := range []map[string]string{
		{},
		{"solver_type": ""},
		{"solver_type": "flaresolverr"}, // deferred to Phase 6
	} {
		var got options
		SolverOption(cfg)(&got)
		if got.solver != nil {
			t.Errorf("SolverOption(%v) solver = %v, want nil (default NoopSolver)", cfg, got.solver)
		}
	}
}
