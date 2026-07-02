package api_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/autobrr/harbrr/internal/web/api"
)

// TestErrorEnvelopeHasCode asserts every error carries a machine-readable code:
// a status-derived default (not_found) and a sentinel-classified one (already_setup).
func TestErrorEnvelopeHasCode(t *testing.T) {
	t.Parallel()
	base, c := serve(t, newEnv(t, api.Config{
		AuthDisabled: true,
		IPAllowlist:  []string{"127.0.0.0/8", "::1/128"},
	}))

	// Status-derived code (writeError -> codeForStatus).
	resp, body := do(t, c, http.MethodGet, base+"/api/indexers/nope/capabilities", nil, nil)
	assertCode(t, resp, body, http.StatusNotFound, "not_found")

	// Sentinel-classified code (writeServiceError): a second setup is already_setup.
	_, _ = do(t, c, http.MethodPost, base+"/api/auth/setup",
		map[string]string{"username": "admin", "password": "correct-horse-staple"}, nil)
	resp, body = do(t, c, http.MethodPost, base+"/api/auth/setup",
		map[string]string{"username": "admin", "password": "correct-horse-staple"}, nil)
	assertCode(t, resp, body, http.StatusConflict, "already_setup")
}

func assertCode(t *testing.T, resp *http.Response, body []byte, wantStatus int, wantCode string) {
	t.Helper()
	if resp.StatusCode != wantStatus {
		t.Fatalf("status = %d, want %d (%s)", resp.StatusCode, wantStatus, body)
	}
	var e struct {
		Error string `json:"error"`
		Code  string `json:"code"`
	}
	if err := json.Unmarshal(body, &e); err != nil {
		t.Fatalf("decode: %v (%s)", err, body)
	}
	if e.Code != wantCode {
		t.Errorf("code = %q, want %q (%s)", e.Code, wantCode, body)
	}
	if e.Error == "" {
		t.Error("error message is empty")
	}
}
