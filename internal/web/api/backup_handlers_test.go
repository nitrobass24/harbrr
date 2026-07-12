package api_test

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/autobrr/harbrr/internal/web/api"
)

// TestBackupExportImportRoundTrip drives export → import through the real router: a
// configured instance exports an encrypted bundle, and re-importing it (force) restores
// the same config. It also covers the passphrase / force / base64 error paths.
func TestBackupExportImportRoundTrip(t *testing.T) {
	t.Parallel()
	base, c := serve(t, newEnv(t, api.Config{}))
	setupAndLogin(t, base, c)

	// Seed one resource so there is config to round-trip.
	resp, body := do(t, c, http.MethodPost, base+"/api/sync-profiles", map[string]any{"name": "tv"}, nil)
	mustStatus(t, resp, body, http.StatusCreated)

	// Export requires a passphrase.
	resp, body = do(t, c, http.MethodPost, base+"/api/export", map[string]string{}, nil)
	mustStatus(t, resp, body, http.StatusBadRequest)

	// A real export returns the bundle as a download.
	resp, bundle := do(t, c, http.MethodPost, base+"/api/export", map[string]string{"passphrase": "pw"}, nil)
	mustStatus(t, resp, bundle, http.StatusOK)
	if cd := resp.Header.Get("Content-Disposition"); !strings.Contains(cd, "attachment") {
		t.Errorf("export Content-Disposition = %q, want an attachment", cd)
	}
	// The bundle is a valid envelope with a sealed (non-empty) payload and no cleartext table data.
	var env struct {
		SchemaVersion int    `json:"schemaVersion"`
		Payload       string `json:"payload"`
	}
	if err := json.Unmarshal(bundle, &env); err != nil {
		t.Fatalf("bundle not JSON: %v", err)
	}
	if env.SchemaVersion != 1 || env.Payload == "" {
		t.Fatalf("unexpected envelope: version=%d payloadEmpty=%v", env.SchemaVersion, env.Payload == "")
	}

	payload := base64.StdEncoding.EncodeToString(bundle)

	// Import into the (now non-empty) instance without force → 409.
	resp, body = do(t, c, http.MethodPost, base+"/api/import",
		map[string]any{"payload": payload, "passphrase": "pw"}, nil)
	mustStatus(t, resp, body, http.StatusConflict)

	// Wrong passphrase → 400.
	resp, body = do(t, c, http.MethodPost, base+"/api/import",
		map[string]any{"payload": payload, "passphrase": "nope", "force": true}, nil)
	mustStatus(t, resp, body, http.StatusBadRequest)

	// A non-base64 payload → 400.
	resp, body = do(t, c, http.MethodPost, base+"/api/import",
		map[string]any{"payload": "!!not base64!!", "passphrase": "pw", "force": true}, nil)
	mustStatus(t, resp, body, http.StatusBadRequest)

	// Force import restores successfully.
	resp, body = do(t, c, http.MethodPost, base+"/api/import",
		map[string]any{"payload": payload, "passphrase": "pw", "force": true}, nil)
	mustStatus(t, resp, body, http.StatusNoContent)

	// The seeded profile survived the wipe-and-load (exactly one, not doubled).
	resp, body = do(t, c, http.MethodGet, base+"/api/sync-profiles", nil, nil)
	mustStatus(t, resp, body, http.StatusOK)
	var profiles []map[string]any
	if err := json.Unmarshal(body, &profiles); err != nil {
		t.Fatalf("unmarshal profiles: %v", err)
	}
	if len(profiles) != 1 || profiles[0]["name"] != "tv" {
		t.Errorf("after restore profiles = %v, want exactly the seeded 'tv'", profiles)
	}
}
