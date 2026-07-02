package api_test

import (
	"net/http"
	"testing"

	"github.com/autobrr/harbrr/internal/web/api"
)

// TestChangePasswordHandler covers the full change-password flow: a wrong current
// password is 401, a weak new password is 400, a valid change is 204, and afterward
// only the new password logs in.
func TestChangePasswordHandler(t *testing.T) {
	t.Parallel()
	base, c := serve(t, newEnv(t, api.Config{}))
	setupAndLogin(t, base, c) // admin / correct-horse-staple

	// Wrong current password -> 401.
	resp, _ := do(t, c, http.MethodPost, base+"/api/auth/change-password",
		map[string]string{"currentPassword": "wrong", "newPassword": "brand-new-passphrase"}, nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("wrong current: status = %d, want 401", resp.StatusCode)
	}

	// Weak new password -> 400.
	resp, _ = do(t, c, http.MethodPost, base+"/api/auth/change-password",
		map[string]string{"currentPassword": "correct-horse-staple", "newPassword": "short"}, nil)
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("weak new: status = %d, want 400", resp.StatusCode)
	}

	// Valid change -> 204.
	resp, body := do(t, c, http.MethodPost, base+"/api/auth/change-password",
		map[string]string{"currentPassword": "correct-horse-staple", "newPassword": "brand-new-passphrase"}, nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("change: status = %d, want 204 (%s)", resp.StatusCode, body)
	}

	// The old password no longer logs in; the new one does (fresh client, no session).
	c2 := &http.Client{}
	resp, _ = do(t, c2, http.MethodPost, base+"/api/auth/login",
		map[string]string{"username": "admin", "password": "correct-horse-staple"}, nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("old password still works: status = %d, want 401", resp.StatusCode)
	}
	resp, _ = do(t, c2, http.MethodPost, base+"/api/auth/login",
		map[string]string{"username": "admin", "password": "brand-new-passphrase"}, nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("new password login: status = %d, want 204", resp.StatusCode)
	}
}
