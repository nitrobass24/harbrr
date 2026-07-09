package api_test

import (
	"net/http"
	"testing"

	"github.com/autobrr/harbrr/internal/web/api"
)

// TestAddIndexerDanglingProxyRefIs400 pins U12-F1 end-to-end: POSTing an indexer
// that references a non-existent proxy trips the FK constraint (foreign_keys=ON)
// deep in the registry, which now classifies it as registry.ErrInvalid so
// writeServiceError returns 400 (invalid client input), not a leaked 500.
func TestAddIndexerDanglingProxyRefIs400(t *testing.T) {
	t.Parallel()

	e := newEnv(t, api.Config{})
	base, c := serve(t, e)
	setupAndLogin(t, base, c)

	add := map[string]any{
		"slug": "tt", "definitionId": "testtracker", "proxyId": 999999,
	}
	resp, body := do(t, c, http.MethodPost, base+"/api/indexers", add, nil)
	mustStatus(t, resp, body, http.StatusBadRequest)
}

// TestAddIndexerDanglingSolverRefIs400 is the same guard for the solver reference.
func TestAddIndexerDanglingSolverRefIs400(t *testing.T) {
	t.Parallel()

	e := newEnv(t, api.Config{})
	base, c := serve(t, e)
	setupAndLogin(t, base, c)

	add := map[string]any{
		"slug": "tt", "definitionId": "testtracker", "solverId": 888888,
	}
	resp, body := do(t, c, http.MethodPost, base+"/api/indexers", add, nil)
	mustStatus(t, resp, body, http.StatusBadRequest)
}
