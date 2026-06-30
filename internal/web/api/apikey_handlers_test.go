package api_test

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/autobrr/harbrr/internal/web/api"
)

// TestAPIKeyListAndDelete covers the GET (list) and DELETE handlers and pins the
// no-leak contract: the list view exposes only metadata, never the key hash or the
// plaintext key.
func TestAPIKeyListAndDelete(t *testing.T) {
	t.Parallel()

	base, c := serve(t, newEnv(t, api.Config{}))
	setupAndLogin(t, base, c)

	// Mint a key (plaintext returned once).
	resp, body := do(t, c, http.MethodPost, base+"/api/apikeys", map[string]string{"name": "sonarr"}, nil)
	mustStatus(t, resp, body, http.StatusCreated)
	var minted struct {
		ID  int64  `json:"id"`
		Key string `json:"key"`
	}
	if err := json.Unmarshal(body, &minted); err != nil || minted.Key == "" || minted.ID == 0 {
		t.Fatalf("mint response missing id/key: %s", body)
	}

	// List returns the key metadata.
	resp, body = do(t, c, http.MethodGet, base+"/api/apikeys", nil, nil)
	mustStatus(t, resp, body, http.StatusOK)
	var list []struct {
		ID   int64  `json:"id"`
		Name string `json:"name"`
	}
	if err := json.Unmarshal(body, &list); err != nil {
		t.Fatalf("decode list: %v (body: %s)", err, body)
	}
	if len(list) != 1 || list[0].ID != minted.ID || list[0].Name != "sonarr" {
		t.Fatalf("list = %s, want exactly the minted key", body)
	}

	// No-leak contract: the list view must never carry the key hash or the plaintext.
	if strings.Contains(strings.ToLower(string(body)), "hash") {
		t.Errorf("list view leaked a hash field: %s", body)
	}
	if strings.Contains(string(body), minted.Key) {
		t.Errorf("list view leaked the plaintext key: %s", body)
	}

	// Delete the key, then the list is empty.
	resp, body = do(t, c, http.MethodDelete, base+"/api/apikeys/"+strconv.FormatInt(minted.ID, 10), nil, nil)
	mustStatus(t, resp, body, http.StatusNoContent)

	resp, body = do(t, c, http.MethodGet, base+"/api/apikeys", nil, nil)
	mustStatus(t, resp, body, http.StatusOK)
	if strings.TrimSpace(string(body)) != "[]" {
		t.Errorf("after delete, list = %s, want []", body)
	}

	// A non-numeric id is a 400, not a 500.
	resp, body = do(t, c, http.MethodDelete, base+"/api/apikeys/not-a-number", nil, nil)
	mustStatus(t, resp, body, http.StatusBadRequest)
}
