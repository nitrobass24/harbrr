package api_test

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/autobrr/harbrr/internal/database"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
	"github.com/autobrr/harbrr/internal/indexer/registry"
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

// echoAPIKeyDoer serves a single search result whose title is the apikey the engine
// was built with, so a search response reveals which instance row (pre- or
// post-restore) actually built the engine that served it.
type echoAPIKeyDoer struct{ apikey string }

func (d echoAPIKeyDoer) Do(req *http.Request) (*http.Response, error) {
	body := fmt.Sprintf(`<!DOCTYPE html><html><body>
<table class="results"><tbody>
<tr><td class="cat" data-cat="1"></td>
<td><a class="title" href="/d?id=1">%s</a></td>
<td><a class="dl" href="/dl?id=1">dl</a></td>
<td class="size">1 GB</td><td class="seeders">1</td><td class="leechers">1</td></tr>
</tbody></table></body></html>`, d.apikey)
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{},
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    req,
	}, nil
}

// TestBackupRestoreInvalidatesCachedEngine is the #346 regression through the real
// router: a restore wipes and re-inserts every indexer instance under a NEW id
// (backup/restore.go), so a resolver that only ever invalidated by slug would keep
// serving the pre-restore engine — bound to the deleted id and its stale settings —
// until process restart. It warms the resolver's cache against one config, restores
// a bundle carrying a DIFFERENT config for the same slug, and asserts the next
// search reflects the restored config, not the stale cached one.
func TestBackupRestoreInvalidatesCachedEngine(t *testing.T) {
	t.Parallel()

	e := newEnvFull(t, api.Config{}, func(db *database.DB) *registry.SearchCache {
		return registry.NewSearchCacheFromConfig(db, newCacheParams(), time.Now, zerolog.Nop())
	}, zerolog.Nop(), registry.WithDoerFactory(func(p registry.ClientParams) (search.Doer, error) {
		return echoAPIKeyDoer{apikey: p.Cfg["apikey"]}, nil
	}))
	base, c := serve(t, e)
	setupAndLogin(t, base, c)

	// Configure "tt" with the key the RESTORED bundle should carry, and export it now.
	add := map[string]any{
		"slug": "tt", "definitionId": "testtracker",
		"settings": map[string]string{"apikey": "restored-key"},
	}
	resp, body := do(t, c, http.MethodPost, base+"/api/indexers", add, nil)
	mustStatus(t, resp, body, http.StatusCreated)

	resp, bundle := do(t, c, http.MethodPost, base+"/api/export", map[string]string{"passphrase": "pw"}, nil)
	mustStatus(t, resp, bundle, http.StatusOK)
	payload := base64.StdEncoding.EncodeToString(bundle)

	// Move the live config to a DIFFERENT key and warm the resolver's cache against
	// it — the pre-restore state a naive restore would otherwise leave stuck.
	resp, body = do(t, c, http.MethodPatch, base+"/api/indexers/tt",
		map[string]any{"settings": map[string]string{"apikey": "stale-key"}}, nil)
	mustStatus(t, resp, body, http.StatusNoContent)

	resp, body = do(t, c, http.MethodGet, base+"/api/indexers/tt/search?q=x", nil, nil)
	mustStatus(t, resp, body, http.StatusOK)
	if title := firstResultTitle(t, body); title != "stale-key" {
		t.Fatalf("pre-restore search title = %q, want the warmed stale-key", title)
	}

	// Restore the earlier bundle (apikey=restored-key); the target is non-empty, so force.
	resp, body = do(t, c, http.MethodPost, base+"/api/import",
		map[string]any{"payload": payload, "passphrase": "pw", "force": true}, nil)
	mustStatus(t, resp, body, http.StatusNoContent)

	// Without the fix the resolver keeps serving the cached engine bound to the
	// deleted pre-restore id and "stale-key"; with it, the next search rebuilds
	// against the restored row and reflects "restored-key".
	resp, body = do(t, c, http.MethodGet, base+"/api/indexers/tt/search?q=x", nil, nil)
	mustStatus(t, resp, body, http.StatusOK)
	if title := firstResultTitle(t, body); title != "restored-key" {
		t.Fatalf("post-restore search title = %q, want restored-key (stale cached engine served instead)", title)
	}
}

// firstResultTitle decodes a /api/indexers/{slug}/search response and returns its
// single result's title.
func firstResultTitle(t *testing.T, body []byte) string {
	t.Helper()
	var resp struct {
		Results []struct {
			Title string `json:"title"`
		} `json:"results"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("decode search response: %v (body: %s)", err, body)
	}
	if len(resp.Results) != 1 {
		t.Fatalf("search results = %+v, want exactly 1", resp.Results)
	}
	return resp.Results[0].Title
}
