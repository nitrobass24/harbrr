// Package smoke holds the LIVE smoke harness engine: the pure parity logic that
// diffs harbrr's Torznab feed against a Prowlarr oracle, plus the search/parse and
// evidence helpers both the build-tagged `make smoke-test` and the `harbrr smoke`
// CLI subcommand share. The engine is decoupled from *testing.T (it takes plain
// args and returns typed results/errors) so it has exactly one definition of "what
// counts as a pass". The build-tagged front-end (smoke_test.go, //go:build smoke)
// and the CLI (cmd/harbrr) both call into it.
//
// Nothing here reaches a live tracker on its own; callers supply an *http.Client and
// the live URLs/keys. Every URL/error that could carry a passkey/apikey is routed
// through internal/http RedactURL/RedactError before it lands in a message.
package smoke

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
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

// Config is the shared smoke configuration: the required harbrr + Prowlarr targets,
// the optional app targets (Sonarr/Radarr/qui), and the search queries. Keys are
// secrets — never log a Config verbatim.
type Config struct {
	HarbrrURL, HarbrrKey     string
	ProwlarrURL, ProwlarrKey string
	SonarrURL, SonarrKey     string
	RadarrURL, RadarrKey     string
	QuiURL, QuiKey           string
	Query, FallbackQuery     string
}

// ParseConfig reads the SMOKE_* environment (via the injected getenv, so tests can
// supply a fake) into a Config. harbrr and Prowlarr are required; the app targets are
// optional (an empty URL means "that app is not configured"). URLs are right-trimmed
// of "/" so a trailing slash never doubles up in an assembled path.
func ParseConfig(getenv func(string) string) (Config, error) {
	get := func(k string) string { return strings.TrimSpace(getenv(k)) }
	or := func(k, def string) string {
		if v := get(k); v != "" {
			return v
		}
		return def
	}
	cfg := Config{
		HarbrrURL:     strings.TrimRight(get("SMOKE_HARBRR_URL"), "/"),
		HarbrrKey:     get("SMOKE_HARBRR_APIKEY"),
		ProwlarrURL:   strings.TrimRight(get("SMOKE_PROWLARR_URL"), "/"),
		ProwlarrKey:   get("SMOKE_PROWLARR_APIKEY"),
		SonarrURL:     strings.TrimRight(get("SMOKE_SONARR_URL"), "/"),
		SonarrKey:     get("SMOKE_SONARR_APIKEY"),
		RadarrURL:     strings.TrimRight(get("SMOKE_RADARR_URL"), "/"),
		RadarrKey:     get("SMOKE_RADARR_APIKEY"),
		QuiURL:        strings.TrimRight(get("SMOKE_QUI_URL"), "/"),
		QuiKey:        get("SMOKE_QUI_APIKEY"),
		Query:         or("SMOKE_QUERY", "test"),
		FallbackQuery: or("SMOKE_QUERY_FALLBACK", "2024"),
	}
	for _, req := range []struct{ name, val string }{
		{"SMOKE_HARBRR_URL", cfg.HarbrrURL},
		{"SMOKE_HARBRR_APIKEY", cfg.HarbrrKey},
		{"SMOKE_PROWLARR_URL", cfg.ProwlarrURL},
		{"SMOKE_PROWLARR_APIKEY", cfg.ProwlarrKey},
	} {
		if req.val == "" {
			return Config{}, fmt.Errorf("smoke: required env %s is empty (see docs/smoke-setup.md)", req.name)
		}
	}
	return cfg, nil
}

// Result is one normalized release for comparison.
type Result struct {
	Title string
	Size  int64
}

// httpGet issues one GET with optional headers, returning the body, the status code,
// and a scrubbed transport error. A transport failure (Go's *url.Error embeds the raw
// URL, apikey query param and all) is redacted before it becomes a message; a non-2xx
// is not an error here — the caller decides whether a given status is fatal or a skip.
func httpGet(ctx context.Context, c *http.Client, rawURL string, headers map[string]string) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, 0, fmt.Errorf("GET %s: %s", apphttp.RedactURL(rawURL), apphttp.RedactError(err))
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := c.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("GET %s: %s", apphttp.RedactURL(rawURL), apphttp.RedactError(err))
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	return data, resp.StatusCode, nil
}

// HarbrrSearch queries harbrr's Torznab feed for a slug+query. It returns the parsed
// results, the HTTP status (so the caller decides whether to skip on a rate-limit or
// fail on another non-200), and any transport/parse error. A non-200 yields nil
// results with a nil error and the status for the caller to act on.
func HarbrrSearch(ctx context.Context, c *http.Client, base, key, slug, query string) ([]Result, int, error) {
	u := fmt.Sprintf("%s/api/indexers/%s/results/torznab/api?t=search&q=%s&apikey=%s",
		base, url.PathEscape(slug), url.QueryEscape(query), url.QueryEscape(key))
	body, status, err := httpGet(ctx, c, u, nil)
	if err != nil {
		return nil, status, err
	}
	if status != http.StatusOK {
		return nil, status, nil
	}
	res, err := ParseTorznab(body)
	return res, status, err
}

// ProwlarrIndexerID resolves a Prowlarr indexer id by its definitionName. found is
// false when the oracle has no such indexer (the caller then skips the differential —
// a missing oracle is not a harbrr failure).
func ProwlarrIndexerID(ctx context.Context, c *http.Client, base, key, defName string) (int, bool, error) {
	body, status, err := httpGet(ctx, c, base+"/api/v1/indexer", map[string]string{"X-Api-Key": key})
	if err != nil {
		return 0, false, err
	}
	if status != http.StatusOK {
		return 0, false, nil
	}
	var idx []struct {
		ID             int    `json:"id"`
		DefinitionName string `json:"definitionName"`
	}
	if err := json.Unmarshal(body, &idx); err != nil {
		return 0, false, fmt.Errorf("parse Prowlarr indexer list: %w", err)
	}
	for _, i := range idx {
		if strings.EqualFold(i.DefinitionName, defName) {
			return i.ID, true, nil
		}
	}
	return 0, false, nil
}

// ProwlarrSearch queries Prowlarr's search API for one indexer id. It returns the
// parsed results, the HTTP status, and any transport/parse error; as with HarbrrSearch
// a non-200 yields nil results and a nil error so the caller can skip on an oracle
// hiccup rather than fail harbrr.
func ProwlarrSearch(ctx context.Context, c *http.Client, base, key string, indexerID int, query string) ([]Result, int, error) {
	u := fmt.Sprintf("%s/api/v1/search?query=%s&indexerIds=%d&type=search",
		base, url.QueryEscape(query), indexerID)
	body, status, err := httpGet(ctx, c, u, map[string]string{"X-Api-Key": key})
	if err != nil {
		return nil, status, err
	}
	if status != http.StatusOK {
		return nil, status, nil
	}
	var rels []struct {
		Title string `json:"title"`
		Size  int64  `json:"size"`
	}
	if err := json.Unmarshal(body, &rels); err != nil {
		return nil, status, fmt.Errorf("parse Prowlarr search: %w", err)
	}
	out := make([]Result, 0, len(rels))
	for _, r := range rels {
		out = append(out, Result{Title: r.Title, Size: r.Size})
	}
	return out, status, nil
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

// ParseTorznab decodes a Torznab RSS feed body into the comparison Results (title +
// size, falling back to the enclosure length when <size> is absent).
func ParseTorznab(body []byte) ([]Result, error) {
	var feed torznabFeed
	if err := xml.Unmarshal(body, &feed); err != nil {
		return nil, fmt.Errorf("parse Torznab feed: %w", err)
	}
	out := make([]Result, 0, len(feed.Channel.Items))
	for _, it := range feed.Channel.Items {
		size := it.Size
		if size == 0 {
			size = it.Enclosure.Length
		}
		out = append(out, Result{Title: it.Title, Size: size})
	}
	return out, nil
}

// --- differential -----------------------------------------------------------

// DiffPass reports whether harbrr's and Prowlarr's result sets agree within
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
func DiffPass(harbrr, prowlarr []Result) (bool, string) {
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

func titleJaccard(a, b []Result) float64 {
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

func titleSet(rs []Result) map[string]struct{} {
	m := make(map[string]struct{}, len(rs))
	for _, r := range rs {
		if n := normalizeTitle(r.Title); n != "" {
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

func firstTitles(rs []Result, n int) []string {
	out := make([]string, 0, n)
	for i := 0; i < len(rs) && i < n; i++ {
		out = append(out, rs[i].Title)
	}
	return out
}

// --- evidence ---------------------------------------------------------------

// EvidenceRecord is the per-tracker smoke result written under testdata/ (which is
// gitignored). It carries titles/counts but NEVER credentials or raw feeds.
type EvidenceRecord struct {
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

// secretTokens are the credential-shaped substrings that must never appear in
// evidence or in a rendered report: their presence in free text (a title, a note) is
// treated as a leak. Shared by ValidateNoSecrets and the report's final scrub.
var secretTokens = []string{"passkey", "apikey", "api_key", "rsskey", "torrent_pass", "cf_clearance", "authkey"}

// ValidateNoSecrets returns an error if any free-text field of the record looks like
// it carries a credential, so an evidence file can never leak a secret even if a
// tracker echoes one into a title. rec.Pattern is a fixed enum label (apikey/form/
// cookie/…) that would always false-positive, so it is not scanned.
func ValidateNoSecrets(rec EvidenceRecord) error {
	check := func(field, v string) error {
		low := strings.ToLower(v)
		for _, tok := range secretTokens {
			if strings.Contains(low, tok) {
				return fmt.Errorf("evidence %s for %s looks like it contains a secret token %q; refusing to write", field, rec.Tracker, tok)
			}
		}
		return nil
	}
	if err := check("notes", rec.Notes); err != nil {
		return err
	}
	if err := check("grab", rec.Grab); err != nil {
		return err
	}
	for _, s := range rec.HarbrrTitles {
		if err := check("harbrrTitle", s); err != nil {
			return err
		}
	}
	for _, s := range rec.ProwlarrTitles {
		if err := check("prowlarrTitle", s); err != nil {
			return err
		}
	}
	return nil
}
