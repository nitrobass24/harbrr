package api_test

import (
	"net/http"
	"testing"

	"github.com/autobrr/harbrr/internal/web/api"
)

// TestTestIndexerNotFound: POST /api/indexers/{slug}/test for an unknown slug is
// a 404 (the registry build fails at lookup before any network call). Uses
// auth-disabled + loopback allowlist so no session/API-key setup is needed.
func TestTestIndexerNotFound(t *testing.T) {
	t.Parallel()
	base, c := serve(t, newEnv(t, api.Config{
		AuthDisabled: true,
		IPAllowlist:  []string{"127.0.0.0/8", "::1/128"},
	}))
	resp, _ := do(t, c, http.MethodPost, base+"/api/indexers/does-not-exist/test", nil, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}
