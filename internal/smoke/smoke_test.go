//go:build smoke

// LIVE smoke + Prowlarr differential. Manual only; never in CI.
//
// Drives a running harbrr daemon like a real *arr: for each configured tracker it
// adds an indexer (creds from env, encrypted by the daemon), searches harbrr's
// Torznab feed, searches Prowlarr's feed for the same tracker+query, and asserts
// the two agree within a tolerance (live data is non-deterministic). Sequential
// with gentle delays; backs off on rate-limit. Captures secret-free evidence.
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

	apphttp "github.com/autobrr/harbrr/internal/http"
)

const (
	// Differential tolerances (live data is non-deterministic; harbrr also
	// category-filters, so its count can be legitimately lower than Prowlarr's).
	countRatioMin   = 0.50 // min(h,p)/max(h,p) >= this
	titleJaccardMin = 0.30 // |intersection| / |union| of normalized titles >= this
	// resultCap is the common Cardigann/Torznab page limit. When BOTH sides return
	// a full page, the results are a sort-dependent WINDOW of a larger set, so a low
	// title overlap reflects differing sort config between the two instances (e.g.
	// DigitalCore's config-driven sort), not a harbrr defect — title Jaccard is not
	// a valid comparison there, so we fall back to count parity with a caveat.
	resultCap = 100

	betweenCallsDelay   = 200 * time.Millisecond // harbrr -> Prowlarr spacing
	betweenTrackerDelay = 500 * time.Millisecond // gentle rate between trackers
	httpTimeout         = 45 * time.Second
)

type config struct {
	harbrrURL, harbrrKey     string
	prowlarrURL, prowlarrKey string
	query, fallbackQuery     string
	trackers                 []trackerCfg
}

type trackerCfg struct {
	slug, defID, prowlarrName, pattern string
	settings                           map[string]string
}

func loadConfig(t *testing.T) config {
	t.Helper()
	must := func(k string) string {
		v := strings.TrimSpace(os.Getenv(k))
		if v == "" {
			t.Fatalf("smoke: required env %s is empty (see docs/smoke-setup.md)", k)
		}
		return v
	}
	or := func(k, def string) string {
		if v := strings.TrimSpace(os.Getenv(k)); v != "" {
			return v
		}
		return def
	}
	cfg := config{
		harbrrURL:     strings.TrimRight(must("SMOKE_HARBRR_URL"), "/"),
		harbrrKey:     must("SMOKE_HARBRR_APIKEY"),
		prowlarrURL:   strings.TrimRight(must("SMOKE_PROWLARR_URL"), "/"),
		prowlarrKey:   must("SMOKE_PROWLARR_APIKEY"),
		query:         or("SMOKE_QUERY", "test"),
		fallbackQuery: or("SMOKE_QUERY_FALLBACK", "2024"),
	}
	for _, spec := range strings.Split(must("SMOKE_TRACKERS"), ",") {
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
		cfg.trackers = append(cfg.trackers, tc)
	}
	return cfg
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

// result is one normalized release for comparison.
type result struct {
	title string
	size  int64
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

			q := cfg.query
			harbrr, skipped := harbrrSearch(t, c, cfg, tr.slug, q)
			if skipped {
				return
			}
			if len(harbrr) == 0 {
				q = cfg.fallbackQuery
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

			rec := evidenceRecord{
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
			pass, notes := diffPass(harbrr, prowlarr)
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
	req, err := http.NewRequestWithContext(context.Background(), method, cfg.harbrrURL+path, r)
	if err != nil {
		t.Fatalf("mgmt %s %s: %v", method, path, err)
	}
	req.Header.Set("X-API-Key", cfg.harbrrKey)
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
	body, status := get(t, c, link)
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
	u := fmt.Sprintf("%s/api/v2.0/indexers/%s/results/torznab/api?t=search&q=%s&apikey=%s",
		cfg.harbrrURL, url.PathEscape(slug), url.QueryEscape(query), url.QueryEscape(cfg.harbrrKey))
	body, status := get(t, c, u)
	if status != http.StatusOK {
		return ""
	}
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

// harbrrSearch queries harbrr's Torznab feed. Returns (results, skipped); skipped
// is true on a rate-limit/anti-bot signal (the test t.Skips rather than hammering).
func harbrrSearch(t *testing.T, c *http.Client, cfg config, slug, query string) ([]result, bool) {
	t.Helper()
	u := fmt.Sprintf("%s/api/v2.0/indexers/%s/results/torznab/api?t=search&q=%s&apikey=%s",
		cfg.harbrrURL, url.PathEscape(slug), url.QueryEscape(query), url.QueryEscape(cfg.harbrrKey))
	body, status := get(t, c, u)
	if status == http.StatusTooManyRequests || status == http.StatusServiceUnavailable {
		t.Skipf("%s: harbrr feed rate-limited (HTTP %d); backing off", slug, status)
		return nil, true
	}
	if status != http.StatusOK {
		t.Fatalf("%s: harbrr feed HTTP %d", slug, status)
	}
	return parseTorznab(t, body), false
}

// harbrrHasDownloadLinks reports whether the harbrr feed carries a non-empty
// <link>/<enclosure> for at least one item (confirms a grabbable release).
func harbrrHasDownloadLinks(t *testing.T, c *http.Client, cfg config, slug, query string) bool {
	t.Helper()
	u := fmt.Sprintf("%s/api/v2.0/indexers/%s/results/torznab/api?t=search&q=%s&apikey=%s",
		cfg.harbrrURL, url.PathEscape(slug), url.QueryEscape(query), url.QueryEscape(cfg.harbrrKey))
	body, status := get(t, c, u)
	if status != http.StatusOK {
		return false
	}
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

// prowlarrSearch resolves the tracker's Prowlarr indexer id (by definitionName)
// then queries Prowlarr's search API.
func prowlarrSearch(t *testing.T, c *http.Client, cfg config, prowlarrName, query string) ([]result, bool) {
	t.Helper()
	id, ok := prowlarrIndexerID(t, c, cfg, prowlarrName)
	if !ok {
		t.Skipf("Prowlarr has no indexer with definitionName %q; skipping differential", prowlarrName)
		return nil, true
	}
	u := fmt.Sprintf("%s/api/v1/search?query=%s&indexerIds=%d&type=search",
		cfg.prowlarrURL, url.QueryEscape(query), id)
	body, status := getProwlarr(t, c, cfg, u)
	if status == http.StatusTooManyRequests || status == http.StatusServiceUnavailable {
		t.Skipf("%s: Prowlarr rate-limited (HTTP %d); backing off", prowlarrName, status)
		return nil, true
	}
	if status != http.StatusOK {
		// The Prowlarr oracle being slow/erroring (timeout -> status 0, a 400, etc.) is
		// not a harbrr failure — skip the differential for this tracker rather than fail.
		t.Skipf("%s: Prowlarr oracle unavailable (HTTP %d); skipping differential", prowlarrName, status)
		return nil, true
	}
	var rels []struct {
		Title string `json:"title"`
		Size  int64  `json:"size"`
	}
	if err := json.Unmarshal(body, &rels); err != nil {
		t.Fatalf("%s: parse Prowlarr search: %v", prowlarrName, err)
	}
	out := make([]result, 0, len(rels))
	for _, r := range rels {
		out = append(out, result{title: r.Title, size: r.Size})
	}
	return out, false
}

func prowlarrIndexerID(t *testing.T, c *http.Client, cfg config, defName string) (int, bool) {
	t.Helper()
	body, status := getProwlarr(t, c, cfg, cfg.prowlarrURL+"/api/v1/indexer")
	if status != http.StatusOK {
		// Oracle list unavailable -> caller skips the differential (not a harbrr failure).
		t.Logf("Prowlarr indexer list unavailable (HTTP %d)", status)
		return 0, false
	}
	var idx []struct {
		ID             int    `json:"id"`
		DefinitionName string `json:"definitionName"`
	}
	if err := json.Unmarshal(body, &idx); err != nil {
		t.Fatalf("parse Prowlarr indexer list: %v", err)
	}
	for _, i := range idx {
		if strings.EqualFold(i.DefinitionName, defName) {
			return i.ID, true
		}
	}
	return 0, false
}

func get(t *testing.T, c *http.Client, u string) ([]byte, int) {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, u, nil)
	if err != nil {
		t.Fatalf("GET %s: %v", apphttp.RedactURL(u), apphttp.RedactError(err))
	}
	resp, err := c.Do(req)
	if err != nil {
		// Go's *url.Error embeds the raw URL (apikey query param), so scrub the error too.
		t.Fatalf("GET %s failed: %v", apphttp.RedactURL(u), apphttp.RedactError(err))
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	return data, resp.StatusCode
}

func getProwlarr(t *testing.T, c *http.Client, cfg config, u string) ([]byte, int) {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, u, nil)
	if err != nil {
		t.Fatalf("Prowlarr GET %s: %v", apphttp.RedactURL(u), apphttp.RedactError(err))
	}
	req.Header.Set("X-Api-Key", cfg.prowlarrKey)
	resp, err := c.Do(req)
	if err != nil {
		// A Prowlarr transport error (e.g. a slow search timing out) is oracle-side, not
		// a harbrr failure; return status 0 so the caller skips rather than fatals.
		t.Logf("Prowlarr GET %s failed: %v", apphttp.RedactURL(u), apphttp.RedactError(err))
		return nil, 0
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	return data, resp.StatusCode
}

// --- Torznab feed parsing ---------------------------------------------------

type torznabFeed struct {
	Channel struct {
		Items []struct {
			Title     string `xml:"title"`
			Link      string `xml:"link"`
			Size      int64  `xml:"size"`
			Enclosure struct {
				URL    string `xml:"url,attr"`
				Length int64  `xml:"length,attr"`
			} `xml:"enclosure"`
		} `xml:"item"`
	} `xml:"channel"`
}

func parseTorznab(t *testing.T, body []byte) []result {
	t.Helper()
	var feed torznabFeed
	if err := xml.Unmarshal(body, &feed); err != nil {
		t.Fatalf("parse harbrr Torznab feed: %v", err)
	}
	out := make([]result, 0, len(feed.Channel.Items))
	for _, it := range feed.Channel.Items {
		size := it.Size
		if size == 0 {
			size = it.Enclosure.Length
		}
		out = append(out, result{title: it.Title, size: size})
	}
	return out
}

// --- differential -----------------------------------------------------------

// diffPass reports whether harbrr's and Prowlarr's result sets agree within
// tolerance. Live data is non-deterministic and harbrr applies category
// filtering, so the test is bounded, not byte-exact:
//
//   - both empty -> PASS (the tracker had nothing for this query)
//   - Prowlarr > 0, harbrr = 0 -> FAIL (harbrr missed everything)
//   - harbrr > 0, Prowlarr = 0 -> PASS (harbrr found results Prowlarr's cache missed)
//   - otherwise: count ratio >= countRatioMin AND title Jaccard >= titleJaccardMin
//
// Example: Prowlarr 40, harbrr 32 -> ratio 0.80 >= 0.50; 20 shared titles ->
// Jaccard 20/52 = 0.38 >= 0.30 -> PASS.
func diffPass(harbrr, prowlarr []result) (bool, string) {
	h, p := len(harbrr), len(prowlarr)
	switch {
	case h == 0 && p == 0:
		return true, "both empty"
	case h == 0 && p > 0:
		return false, fmt.Sprintf("harbrr returned 0 while Prowlarr returned %d", p)
	case p == 0 && h > 0:
		return true, fmt.Sprintf("harbrr %d, Prowlarr 0 (likely a Prowlarr cache miss)", h)
	}
	ratio := float64(min(h, p)) / float64(max(h, p))
	jac := titleJaccard(harbrr, prowlarr)
	if ratio < countRatioMin {
		return false, fmt.Sprintf("count ratio %.2f < %.2f (harbrr %d, Prowlarr %d)", ratio, countRatioMin, h, p)
	}
	if jac >= titleJaccardMin {
		return true, fmt.Sprintf("count ratio %.2f, title Jaccard %.2f", ratio, jac)
	}
	// Low title overlap but a full page on both sides: a sort-dependent window of a
	// larger result set (config-driven sort differs between harbrr and Prowlarr).
	// Titles aren't comparable here; accept on strong count parity with a caveat.
	if h >= resultCap && p >= resultCap && ratio >= 0.90 {
		return true, fmt.Sprintf("count parity %.2f at the %d-result page cap; titles incomparable (config-sorted window, Jaccard %.2f)", ratio, resultCap, jac)
	}
	return false, fmt.Sprintf("title Jaccard %.2f < %.2f (harbrr %d, Prowlarr %d)", jac, titleJaccardMin, h, p)
}

func titleJaccard(a, b []result) float64 {
	sa, sb := titleSet(a), titleSet(b)
	if len(sa) == 0 && len(sb) == 0 {
		return 1
	}
	inter := 0
	for k := range sa {
		if _, ok := sb[k]; ok {
			inter++
		}
	}
	union := len(sa) + len(sb) - inter
	if union == 0 {
		return 0
	}
	return float64(inter) / float64(union)
}

func titleSet(rs []result) map[string]struct{} {
	m := make(map[string]struct{}, len(rs))
	for _, r := range rs {
		if n := normalizeTitle(r.title); n != "" {
			m[n] = struct{}{}
		}
	}
	return m
}

// normalizeTitle lowercases, keeps letters/digits/spaces, and collapses runs of
// whitespace, so cosmetic punctuation/case differences don't sink the Jaccard.
func normalizeTitle(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteByte(' ')
		}
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

func firstTitles(rs []result, n int) []string {
	out := make([]string, 0, n)
	for i := 0; i < len(rs) && i < n; i++ {
		out = append(out, rs[i].title)
	}
	return out
}

// --- evidence ---------------------------------------------------------------

// evidenceRecord is the per-tracker smoke result written under testdata/ (which
// is gitignored). It carries titles/counts but NEVER credentials or raw feeds.
type evidenceRecord struct {
	Tracker              string   `json:"tracker"`
	Pattern              string   `json:"pattern,omitempty"`
	TestOK               bool     `json:"testOk"`
	Query                string   `json:"query"`
	HarbrrCount          int      `json:"harbrrCount"`
	ProwlarrCount        int      `json:"prowlarrCount"`
	HarbrrTitles         []string `json:"harbrrTitles"`
	ProwlarrTitles       []string `json:"prowlarrTitles"`
	DownloadLinksPresent bool     `json:"downloadLinksPresent"`
	Grab                 string   `json:"grab,omitempty"`
	Pass                 bool     `json:"pass"`
	Notes                string   `json:"notes"`
}

func writeEvidence(t *testing.T, rec evidenceRecord) {
	t.Helper()
	validateNoSecrets(t, rec)
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

// validateNoSecrets fails the test BEFORE writing if any string field looks like
// it carries a credential, so an evidence file can never leak a secret even if a
// tracker echoes one into a title.
func validateNoSecrets(t *testing.T, rec evidenceRecord) {
	t.Helper()
	tokens := []string{"passkey", "apikey", "api_key", "rsskey", "torrent_pass", "cf_clearance", "authkey"}
	check := func(field, v string) {
		low := strings.ToLower(v)
		for _, tok := range tokens {
			if strings.Contains(low, tok) {
				t.Fatalf("evidence %s for %s looks like it contains a secret token %q; refusing to write", field, rec.Tracker, tok)
			}
		}
	}
	check("notes", rec.Notes)
	check("grab", rec.Grab)
	// rec.Pattern is a fixed enum label (apikey/form/cookie/cloudflare/proxy/avistaz),
	// not data — and "apikey"/"cookie" are themselves secret-token substrings, so it
	// would always false-positive. It carries no secret, so it is not scanned.
	for _, s := range rec.HarbrrTitles {
		check("harbrrTitle", s)
	}
	for _, s := range rec.ProwlarrTitles {
		check("prowlarrTitle", s)
	}
}
