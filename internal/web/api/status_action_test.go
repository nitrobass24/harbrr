package api_test

import (
	"net/http"
	"testing"

	"github.com/autobrr/harbrr/internal/web/api"
)

// TestIndexerStatusNotFound: GET /api/indexers/{slug}/status for an unknown slug is
// a 404. Uses auth-disabled + loopback allowlist so no session/API-key setup is
// needed. The populated-status path is covered at the registry layer
// (TestSearchRecordsHealthEvent).
func TestIndexerStatusNotFound(t *testing.T) {
	t.Parallel()
	base, c := serve(t, newEnv(t, api.Config{
		AuthDisabled: true,
		IPAllowlist:  []string{"127.0.0.0/8", "::1/128"},
	}))
	resp, _ := do(t, c, http.MethodGet, base+"/api/indexers/does-not-exist/status", nil, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}
