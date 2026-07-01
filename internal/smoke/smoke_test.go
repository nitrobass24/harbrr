//go:build smoke

// LIVE smoke + Prowlarr differential. Manual only; never in CI.
//
// Drives a running harbrr daemon like a real *arr: for each configured tracker it
// adds an indexer (creds from env, encrypted by the daemon), searches harbrr's
// Torznab feed, searches Prowlarr's feed for the same tracker+query, and asserts
// the two agree within a tolerance (live data is non-deterministic). Sequential
// with gentle delays; backs off on rate-limit. Captures secret-free evidence.
//
// The pure parity engine (Config/ParseConfig, Result/DiffPass, the search/parse and
// evidence helpers) lives in engine.go — this file is only the *testing.T front-end
// that keeps the per-tracker credential-setup path the CLI does not need.
//
// Required env (see docs/smoke-setup.md):
//
//	SMOKE_HARBRR_URL, SMOKE_HARBRR_APIKEY
//	SMOKE_PROWLARR_URL, SMOKE_PROWLARR_APIKEY
//	SMOKE_TRACKERS = "slug|defId|prowlarrName[|pattern],..."   (pattern is a free
//	   label recorded in evidence: apikey | form | cookie | netquirk | cloudflare |
//	   proxy | avistaz)
//	Per-tracker credentials/settings — one of:
//	  SMOKE_SETTINGS_<SLUG> = a JSON object of harbrr settings, e.g.
//	      {"apikey":"…"}
//	      {"cookie":"…","solver_type":"manual_cookie"}
//	      {"solver_type":"flaresolverr","flaresolverr_url":"http://flaresolverr:8191"}
//	      {"proxy_type":"socks5","proxy_url":"socks5://host:1080"}
//	      {"username":"…","password":"…","pid":"…"}   (AvistaZ family)
//	  SMOKE_KEY_<SLUG>      = shorthand for {"apikey":"…"}   (back-compat)
//	  (SLUG upper-cased; - and . -> _)
//	SMOKE_QUERY (optional, default "test"), SMOKE_QUERY_FALLBACK (default "2024")
//	SMOKE_GRAB=1 (optional) — also resolve the first release's link to a real
//	   .torrent/magnet (the qBittorrent push + seeding stays a manual, no-H&R step).
package smoke

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"
)

// config is the smoke test's configuration: the shared engine Config plus the
// per-tracker credential list the *testing.T harness sets up (the CLI reads harbrr's
// already-configured indexers instead, so it needs no trackers).
type config struct {
	Config
	trackers []trackerCfg
}

type trackerCfg struct {
	slug, defID, prowlarrName, pattern string
	settings                           map[string]string
}

func loadConfig(t *testing.T) config {
	t.Helper()
	base, err := ParseConfig(os.Getenv)
	if err != nil {
		t.Fatalf("smoke: %v", err)
	}
	cfg := config{Config: base}
	spec := strings.TrimSpace(os.Getenv("SMOKE_TRACKERS"))
	if spec == "" {
		t.Fatalf("smoke: required env SMOKE_TRACKERS is empty (see docs/smoke-setup.md)")
	}
	for _, entry := range strings.Split(spec, ",") {
		cfg.trackers = append(cfg.trackers, parseTracker(t, entry))
	}
	return cfg
}

func parseTracker(t *testing.T, spec string) trackerCfg {
	t.Helper()
	parts := strings.Split(strings.TrimSpace(spec), "|")
	if len(parts) < 3 || len(parts) > 4 {
		t.Fatalf("smoke: SMOKE_TRACKERS entry %q must be slug|defId|prowlarrName[|pattern]", spec)
	}
	slug := strings.TrimSpace(parts[0])
	defID := strings.TrimSpace(parts[1])
	prowlarrName := strings.TrimSpace(parts[2])
	if slug == "" || defID == "" || prowlarrName == "" {
		t.Fatalf("smoke: SMOKE_TRACKERS entry %q has an empty slug/defId/prowlarrName", spec)
	}
	tc := trackerCfg{slug: slug, defID: defID, prowlarrName: prowlarrName, settings: loadSettings(t, slug)}
	if len(parts) == 4 {
		tc.pattern = strings.TrimSpace(parts[3])
	}
	return tc
}

// loadSettings reads a tracker's harbrr settings: SMOKE_SETTINGS_<SLUG> (a JSON
// object — any harbrr setting: apikey/cookie/username/password/pid/solver_type/
// proxy_type/…) or SMOKE_KEY_<SLUG> (apikey shorthand, back-compat).
func loadSettings(t *testing.T, slug string) map[string]string {
	t.Helper()
	env := envSanitize(slug)
	if raw := strings.TrimSpace(os.Getenv("SMOKE_SETTINGS_" + env)); raw != "" {
		var m map[string]string
		if err := json.Unmarshal([]byte(raw), &m); err != nil {
			t.Fatalf("smoke: SMOKE_SETTINGS_%s must be a JSON object of string settings: %v", env, err)
		}
		return m
	}
	if key := strings.TrimSpace(os.Getenv("SMOKE_KEY_" + env)); key != "" {
		return map[string]string{"apikey": key}
	}
	t.Fatalf("smoke: tracker %q needs SMOKE_SETTINGS_%s (JSON) or SMOKE_KEY_%s (apikey)", slug, env, env)
	return nil
}

func envSanitize(slug string) string {
	return strings.NewReplacer("-", "_", ".", "_").Replace(strings.ToUpper(slug))
}

func TestSmoke(t *testing.T) {
	cfg := loadConfig(t)
	c := &http.Client{Timeout: httpTimeout}

	for i, tr := range cfg.trackers {
		t.Run(tr.slug, func(t *testing.T) {
			// Sequential ON PURPOSE — no t.Parallel: gentle, predictable rate.
			setupIndexer(t, c, cfg, tr)

			// Live login/connectivity probe (the management Test action). For a
			// credentialed pattern (form/cookie/CF/proxy/avistaz) a passing test is the
			// key live confirmation; the differential below is the result-set gate.
			testOK, found := testIndexer(t, c, cfg, tr.slug)
			if !found {
				t.Fatalf("%s: indexer not found immediately after add", tr.slug)
			}
			if !testOK {
				t.Logf("%s: WARNING — Test action (login probe) did not pass; search may be empty", tr.slug)
			}

			q := cfg.Query
			harbrr, skipped := harbrrSearch(t, c, cfg, tr.slug, q)
			if skipped {
				return
			}
			if len(harbrr) == 0 {
				q = cfg.FallbackQuery
				harbrr, skipped = harbrrSearch(t, c, cfg, tr.slug, q)
				if skipped {
					return
				}
			}
			time.Sleep(betweenCallsDelay)
			prowlarr, pSkipped := prowlarrSearch(t, c, cfg, tr.prowlarrName, q)
			if pSkipped {
				return
			}

			rec := EvidenceRecord{
				Tracker:              tr.slug,
				Pattern:              tr.pattern,
				TestOK:               testOK,
				Query:                q,
				HarbrrCount:          len(harbrr),
				ProwlarrCount:        len(prowlarr),
				HarbrrTitles:         firstTitles(harbrr, 5),
				ProwlarrTitles:       firstTitles(prowlarr, 5),
				DownloadLinksPresent: false, // set below via the raw feed check
			}
			pass, notes := DiffPass(harbrr, prowlarr)
			rec.Pass, rec.Notes = pass, notes
			rec.DownloadLinksPresent = harbrrHasDownloadLinks(t, c, cfg, tr.slug, q)
			if os.Getenv("SMOKE_GRAB") == "1" {
				rec.Grab = grabResolve(t, c, cfg, tr.slug, q)
			}

			writeEvidence(t, rec)
			t.Logf("%s: harbrr=%d prowlarr=%d pass=%v (%s)", tr.slug, len(harbrr), len(prowlarr), pass, notes)
			if !pass {
				t.Errorf("differential FAILED for %s: %s", tr.slug, notes)
			}
			if i < len(cfg.trackers)-1 {
				time.Sleep(betweenTrackerDelay)
			}
		})
	}
}

// setupIndexer adds the tracker to harbrr (creds encrypted by the daemon) and
// registers a t.Cleanup to remove it, so re-runs are idempotent. A failed add is
// fatal for this tracker (never proceed with a half-configured instance).
func setupIndexer(t *testing.T, c *http.Client, cfg config, tr trackerCfg) {
	t.Helper()
	body, _ := json.Marshal(map[string]any{
		"slug":         tr.slug,
		"definitionId": tr.defID,
		"name":         tr.slug,
		"settings":     tr.settings,
	})
	// Delete first (idempotent) then add.
	_ = mgmt(t, c, cfg, http.MethodDelete, "/api/indexers/"+tr.slug, nil)
	if code := mgmt(t, c, cfg, http.MethodPost, "/api/indexers", body); code != http.StatusCreated && code != http.StatusOK {
		t.Fatalf("setup %s: POST /api/indexers = %d", tr.slug, code)
	}
	t.Cleanup(func() {
		_ = mgmt(t, c, cfg, http.MethodDelete, "/api/indexers/"+tr.slug, nil)
	})
}

// mgmt issues a management API call with the X-API-Key header and returns the
// status code (the body is read but discarded).
func mgmt(t *testing.T, c *http.Client, cfg config, method, path string, body []byte) int {
	t.Helper()
	_, status := mgmtBody(t, c, cfg, method, path, body)
	return status
}

// mgmtBody is mgmt, but also returns the response body (for the Test action). The
// request body (which may carry creds) is never logged; the caller must not log a
// response body that could echo one.
func mgmtBody(t *testing.T, c *http.Client, cfg config, method, path string, body []byte) ([]byte, int) {
	t.Helper()
	var r io.Reader
	if body != nil {
		r = strings.NewReader(string(body))
	}
	req, err := http.NewRequestWithContext(context.Background(), method, cfg.HarbrrURL+path, r)
	if err != nil {
		t.Fatalf("mgmt %s %s: %v", method, path, err)
	}
	req.Header.Set("X-API-Key", cfg.HarbrrKey)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("mgmt %s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	return data, resp.StatusCode
}

// testIndexer runs the management Test action (the login/connectivity probe against
// a fresh engine) and returns whether it passed and whether the slug exists. The
// endpoint scrubs its error; we keep only the boolean, so evidence never carries a
// secret.
func testIndexer(t *testing.T, c *http.Client, cfg config, slug string) (ok, found bool) {
	t.Helper()
	body, status := mgmtBody(t, c, cfg, http.MethodPost, "/api/indexers/"+url.PathEscape(slug)+"/test", nil)
	switch status {
	case http.StatusNotFound:
		return false, false
	case http.StatusOK:
		var res struct {
			OK bool `json:"ok"`
		}
		if err := json.Unmarshal(body, &res); err != nil {
			t.Fatalf("test %s: decode /test response: %v", slug, err)
		}
		return res.OK, true
	default:
		return false, true
	}
}

// grabResolve fetches the first served release's download link and confirms a real
// .torrent (bencode) or a magnet — proving the grab path resolves end to end. It does
// NOT push to qBittorrent; the no-hit-and-run seeding step stays a manual confirmation
// (see README). Gated by SMOKE_GRAB since it pulls a real .torrent from the tracker.
// The returned note is a fixed label (no secret).
func grabResolve(t *testing.T, c *http.Client, cfg config, slug, query string) string {
	t.Helper()
	link := firstDownloadLink(t, c, cfg, slug, query)
	switch {
	case link == "":
		return "no download link"
	case strings.HasPrefix(link, "magnet:"):
		return "magnet"
	}
	body, status, err := httpGet(context.Background(), c, link, nil)
	if err != nil {
		t.Fatalf("grab %s: %v", slug, err)
	}
	if status != http.StatusOK {
		return fmt.Sprintf("download HTTP %d", status)
	}
	if len(body) > 0 && body[0] == 'd' { // a bencoded torrent dict starts with 'd'
		return "torrent"
	}
	return "not a torrent/magnet"
}

// firstDownloadLink returns the first feed item's link/enclosure — a /dl proxy URL
// for a resolver-needing tracker, or the direct tracker link otherwise.
func firstDownloadLink(t *testing.T, c *http.Client, cfg config, slug, query string) string {
	t.Helper()
	body := harbrrFeedBody(t, c, cfg, slug, query)
	var feed torznabFeed
	if err := xml.Unmarshal(body, &feed); err != nil {
		return ""
	}
	for _, it := range feed.Channel.Items {
		if l := strings.TrimSpace(it.Link); l != "" {
			return l
		}
		if l := strings.TrimSpace(it.Enclosure.URL); l != "" {
			return l
		}
	}
	return ""
}

// harbrrHasDownloadLinks reports whether the harbrr feed carries a non-empty
// <link>/<enclosure> for at least one item (confirms a grabbable release).
func harbrrHasDownloadLinks(t *testing.T, c *http.Client, cfg config, slug, query string) bool {
	t.Helper()
	body := harbrrFeedBody(t, c, cfg, slug, query)
	var feed torznabFeed
	if err := xml.Unmarshal(body, &feed); err != nil {
		return false
	}
	for _, it := range feed.Channel.Items {
		if strings.TrimSpace(it.Link) != "" || strings.TrimSpace(it.Enclosure.URL) != "" {
			return true
		}
	}
	return false
}

// harbrrFeedBody fetches the raw Torznab feed body for a slug+query (used by the
// download-link probes, which need the item <link>/<enclosure> the parsed Result set
// discards). A non-200 or transport error yields an empty body (the probes then
// report "no link").
func harbrrFeedBody(t *testing.T, c *http.Client, cfg config, slug, query string) []byte {
	t.Helper()
	u := fmt.Sprintf("%s/api/indexers/%s/results/torznab/api?t=search&q=%s&apikey=%s",
		cfg.HarbrrURL, url.PathEscape(slug), url.QueryEscape(query), url.QueryEscape(cfg.HarbrrKey))
	body, status, err := httpGet(context.Background(), c, u, nil)
	if err != nil || status != http.StatusOK {
		return nil
	}
	return body
}

// harbrrSearch queries harbrr's Torznab feed. Returns (results, skipped); skipped
// is true on a rate-limit/anti-bot signal (the test t.Skips rather than hammering).
func harbrrSearch(t *testing.T, c *http.Client, cfg config, slug, query string) ([]Result, bool) {
	t.Helper()
	res, status, err := HarbrrSearch(context.Background(), c, cfg.HarbrrURL, cfg.HarbrrKey, slug, query)
	if status == http.StatusTooManyRequests || status == http.StatusServiceUnavailable {
		t.Skipf("%s: harbrr feed rate-limited (HTTP %d); backing off", slug, status)
		return nil, true
	}
	if err != nil {
		t.Fatalf("%s: harbrr feed: %v", slug, err)
	}
	if status != http.StatusOK {
		t.Fatalf("%s: harbrr feed HTTP %d", slug, status)
	}
	return res, false
}

// prowlarrSearch resolves the tracker's Prowlarr indexer id (by definitionName)
// then queries Prowlarr's search API.
func prowlarrSearch(t *testing.T, c *http.Client, cfg config, prowlarrName, query string) ([]Result, bool) {
	t.Helper()
	id, ok, err := ProwlarrIndexerID(context.Background(), c, cfg.ProwlarrURL, cfg.ProwlarrKey, prowlarrName)
	if err != nil {
		// A Prowlarr transport error is oracle-side, not a harbrr failure — skip.
		t.Skipf("%s: Prowlarr oracle unavailable (%v); skipping differential", prowlarrName, err)
		return nil, true
	}
	if !ok {
		t.Skipf("Prowlarr has no indexer with definitionName %q; skipping differential", prowlarrName)
		return nil, true
	}
	res, status, err := ProwlarrSearch(context.Background(), c, cfg.ProwlarrURL, cfg.ProwlarrKey, id, query)
	if err != nil {
		t.Skipf("%s: Prowlarr oracle unavailable (%v); skipping differential", prowlarrName, err)
		return nil, true
	}
	if status == http.StatusTooManyRequests || status == http.StatusServiceUnavailable {
		t.Skipf("%s: Prowlarr rate-limited (HTTP %d); backing off", prowlarrName, status)
		return nil, true
	}
	if status != http.StatusOK {
		// The Prowlarr oracle being slow/erroring (timeout, a 400, etc.) is not a harbrr
		// failure — skip the differential for this tracker rather than fail.
		t.Skipf("%s: Prowlarr oracle unavailable (HTTP %d); skipping differential", prowlarrName, status)
		return nil, true
	}
	return res, false
}

// --- evidence ---------------------------------------------------------------

// writeEvidence validates the record carries no secret, then writes it under the
// gitignored testdata/ directory as pretty JSON.
func writeEvidence(t *testing.T, rec EvidenceRecord) {
	t.Helper()
	if err := ValidateNoSecrets(rec); err != nil {
		t.Fatalf("%v", err)
	}
	if err := os.MkdirAll("testdata", 0o750); err != nil {
		t.Fatalf("evidence dir: %v", err)
	}
	data, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		t.Fatalf("marshal evidence: %v", err)
	}
	path := "testdata/smoke-" + rec.Tracker + ".json"
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write evidence: %v", err)
	}
}
