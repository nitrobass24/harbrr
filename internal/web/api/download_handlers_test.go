package api_test

import (
	"net/http"
	"testing"

	"github.com/autobrr/harbrr/internal/web/api"
)

// TestDownloadReleaseRequiresAuth: the session-authed management download route
// (/api/indexers/{slug}/download/{token}) rejects an unauthenticated request. This is
// the whole point of #7 Part A — the web UI authenticates by session cookie, and a
// caller with no session (or key) must get a 401, never a silent pass. The resolve/
// stream behavior on the authenticated path is covered by torznabhttp.TestServeGrab.
func TestDownloadReleaseRequiresAuth(t *testing.T) {
	t.Parallel()
	base, c := serve(t, newEnv(t, api.Config{}))
	resp, _ := do(t, c, http.MethodGet, base+"/api/indexers/demo/download/sometoken", nil, nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated download: status = %d, want 401", resp.StatusCode)
	}
}

// TestDownloadReleaseUnknownIndexer: an AUTHENTICATED request reaches the handler and a
// slug with no configured indexer resolves to a 404 — proving the route is wired under
// the authenticated group and dispatches to downloadRelease. Auth-disabled + a loopback
// allowlist authorizes the request without session/key setup.
func TestDownloadReleaseUnknownIndexer(t *testing.T) {
	t.Parallel()
	base, c := serve(t, newEnv(t, api.Config{AuthDisabled: true, IPAllowlist: []string{"127.0.0.0/8", "::1/128"}}))
	resp, _ := do(t, c, http.MethodGet, base+"/api/indexers/nope/download/sometoken", nil, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown indexer download: status = %d, want 404", resp.StatusCode)
	}
}
