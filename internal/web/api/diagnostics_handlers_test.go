package api_test

import (
	"net/http"
	"testing"

	"github.com/autobrr/harbrr/internal/web/api"
)

// TestIndexerDiagnosticsEndpoint covers the endpoint's two structural answers: an
// unknown slug is a 404, and the management API's auth posture applies (no
// session/API key ⇒ 401, same as every other /api/indexers surface). The populated
// shape is asserted against the rendered JSON in TestDiagnosticsResponseJSON, and
// the capture contents at the engine layer.
func TestIndexerDiagnosticsEndpoint(t *testing.T) {
	t.Parallel()

	t.Run("unknown slug is 404", func(t *testing.T) {
		t.Parallel()
		base, c := serve(t, newEnv(t, api.Config{
			AuthDisabled: true,
			IPAllowlist:  []string{"127.0.0.0/8", "::1/128"},
		}))
		resp, _ := do(t, c, http.MethodGet, base+"/api/indexers/does-not-exist/diagnostics", nil, nil)
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("status = %d, want 404", resp.StatusCode)
		}
	})

	t.Run("requires authentication", func(t *testing.T) {
		t.Parallel()
		base, c := serve(t, newEnv(t, api.Config{}))
		resp, body := do(t, c, http.MethodGet, base+"/api/indexers/anything/diagnostics", nil, nil)
		mustStatus(t, resp, body, http.StatusUnauthorized)
	})
}
