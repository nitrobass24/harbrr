package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
		"harbrrUrl": "http://harbrr:7478",
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

// TestAnnounceConnectionUpdateAndTest drives PATCH + test end-to-end through the router:
// a PATCH repoints the base URL at a stub qui (keeping the stored key, since apiKey is
// omitted), the Test action then probes that repointed URL and passes, and both an
// invalid PATCH and a test against an unknown id map to the right status.
func TestAnnounceConnectionUpdateAndTest(t *testing.T) {
	t.Parallel()
	// A stub qui answering the non-mutating webhook/check a probe sends. apply must
	// never be reached by a probe.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/cross-seed/apply" {
			t.Error("probe/test must not call apply")
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"canCrossSeed": false, "recommendation": "skip"})
	}))
	defer srv.Close()

	base, c := serve(t, newEnv(t, api.Config{}))
	setupAndLogin(t, base, c)

	create := map[string]string{
		"name": "qui", "kind": "qui", "baseUrl": "http://qui:7476", "apiKey": "qui_secret",
		"harbrrUrl": "http://harbrr:7478",
	}
	resp, body := do(t, c, http.MethodPost, base+"/api/announce-connections", create, nil)
	mustStatus(t, resp, body, http.StatusCreated)

	// PATCH: rename and repoint the base URL at the stub; apiKey omitted keeps the key.
	patch := map[string]any{"name": "qui-renamed", "baseUrl": srv.URL}
	resp, body = do(t, c, http.MethodPatch, base+"/api/announce-connections/1", patch, nil)
	mustStatus(t, resp, body, http.StatusNoContent)

	resp, body = do(t, c, http.MethodGet, base+"/api/announce-connections/1", nil, nil)
	mustStatus(t, resp, body, http.StatusOK)
	var got struct {
		Name    string `json:"name"`
		BaseURL string `json:"baseUrl"`
		APIKey  string `json:"apiKey"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Name != "qui-renamed" || got.BaseURL != srv.URL {
		t.Errorf("patch not applied: %+v", got)
	}
	if got.APIKey != secrets.Redacted {
		t.Errorf("apiKey = %q, want redacted after patch", got.APIKey)
	}

	// A non-absolute base URL is a 400 (and leaves the stored URL intact).
	resp, body = do(t, c, http.MethodPatch, base+"/api/announce-connections/1", map[string]any{"baseUrl": "nope"}, nil)
	mustStatus(t, resp, body, http.StatusBadRequest)

	// Test probes the repointed stub (reachable, key kept) → ok:true.
	resp, body = do(t, c, http.MethodPost, base+"/api/announce-connections/1/test", nil, nil)
	mustStatus(t, resp, body, http.StatusOK)
	var res struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(body, &res); err != nil {
		t.Fatalf("unmarshal test result: %v", err)
	}
	if !res.OK {
		t.Errorf("test result ok = false, want true (stub reachable): %q", res.Error)
	}

	// Test against an unknown id is a 404.
	resp, body = do(t, c, http.MethodPost, base+"/api/announce-connections/999/test", nil, nil)
	mustStatus(t, resp, body, http.StatusNotFound)
}
