package api_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/autobrr/harbrr/internal/web/api"
)

// TestGetDefinitionDetail returns the settings-field schema (with the secret flag)
// and capabilities for a known definition; an unknown id is a 404. Uses
// auth-disabled + loopback allowlist so no session/API-key setup is needed.
func TestGetDefinitionDetail(t *testing.T) {
	t.Parallel()
	base, c := serve(t, newEnv(t, api.Config{
		AuthDisabled: true,
		IPAllowlist:  []string{"127.0.0.0/8", "::1/128"},
	}))

	resp, body := do(t, c, http.MethodGet, base+"/api/definitions/testtracker", nil, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", resp.StatusCode, body)
	}
	var dd struct {
		ID       string `json:"id"`
		Settings []struct {
			Name   string `json:"name"`
			Type   string `json:"type"`
			Secret bool   `json:"secret"`
		} `json:"settings"`
		Caps struct {
			Modes map[string][]string `json:"modes"`
		} `json:"caps"`
	}
	if err := json.Unmarshal(body, &dd); err != nil {
		t.Fatalf("decode: %v (%s)", err, body)
	}
	if dd.ID != "testtracker" {
		t.Errorf("id = %q, want testtracker", dd.ID)
	}
	// The apikey field is a text-typed credential -> secret.
	apikeySecret := false
	found := false
	for _, s := range dd.Settings {
		if s.Name == "apikey" {
			apikeySecret = s.Secret
			found = true
		}
	}
	if !found {
		t.Fatalf("apikey setting missing: %+v", dd.Settings)
	}
	if !apikeySecret {
		t.Errorf("apikey should be marked secret")
	}
	if _, ok := dd.Caps.Modes["search"]; !ok {
		t.Errorf("caps missing the search mode: %+v", dd.Caps.Modes)
	}

	// Unknown id -> 404.
	resp, _ = do(t, c, http.MethodGet, base+"/api/definitions/does-not-exist", nil, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("unknown definition: status = %d, want 404", resp.StatusCode)
	}
}
