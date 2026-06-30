package api_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/autobrr/harbrr/internal/secrets"
	"github.com/autobrr/harbrr/internal/web/api"
)

// TestAnnounceConnectionCRUD covers create → list → get → disable → delete, asserting the
// tool API key is redacted on read.
func TestAnnounceConnectionCRUD(t *testing.T) {
	t.Parallel()
	base, c := serve(t, newEnv(t, api.Config{}))
	setupAndLogin(t, base, c)

	// Create a qui announce target.
	create := map[string]string{
		"name": "qui x-seed", "kind": "qui", "baseUrl": "http://qui:7476", "apiKey": "qui_secret",
		"harbrrUrl": "http://harbrr:7474",
	}
	resp, body := do(t, c, http.MethodPost, base+"/api/announce-connections", create, nil)
	mustStatus(t, resp, body, http.StatusCreated)

	var created struct {
		ID     int64  `json:"id"`
		Kind   string `json:"kind"`
		APIKey string `json:"apiKey"`
	}
	if err := json.Unmarshal(body, &created); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if created.ID == 0 || created.Kind != "qui" {
		t.Fatalf("created = %+v", created)
	}
	if created.APIKey != secrets.Redacted {
		t.Errorf("apiKey = %q, want redacted", created.APIKey)
	}

	// List shows it.
	resp, body = do(t, c, http.MethodGet, base+"/api/announce-connections", nil, nil)
	mustStatus(t, resp, body, http.StatusOK)
	var list []map[string]any
	if err := json.Unmarshal(body, &list); err != nil {
		t.Fatalf("unmarshal list: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("list len = %d, want 1", len(list))
	}

	// cross-seed v6 without a harbrrUrl is a 400 (it must fetch the /dl link).
	badCS := map[string]string{"name": "cs", "kind": "crossseed-v6", "baseUrl": "http://cs:2468", "apiKey": "k"}
	resp, body = do(t, c, http.MethodPost, base+"/api/announce-connections", badCS, nil)
	mustStatus(t, resp, body, http.StatusBadRequest)

	// Disable, then delete.
	resp, body = do(t, c, http.MethodPost, base+"/api/announce-connections/1/disable", nil, nil)
	mustStatus(t, resp, body, http.StatusNoContent)
	resp, body = do(t, c, http.MethodDelete, base+"/api/announce-connections/1", nil, nil)
	mustStatus(t, resp, body, http.StatusNoContent)
	resp, body = do(t, c, http.MethodGet, base+"/api/announce-connections/1", nil, nil)
	mustStatus(t, resp, body, http.StatusNotFound)
}
