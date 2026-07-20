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
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
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

	// Field-differential tolerances. The stable fields (size/category/download-url)
	// are compared on every run; the volatile ones (seeders/publishDate) only when
	// Config.StrictFields is set, because they move between the harbrr and Prowlarr
	// fetch and would otherwise flap. All comparisons run ONLY on titles present in
	// both sets, and a field either side leaves unpopulated is not-comparable (never
	// a FAIL) — so the field diff is non-flaky by construction.
	// A size disagreement must exceed BOTH a relative and an absolute budget to be a
	// divergence. The absolute budget absorbs 1-decimal "X.Y GB" display rounding
	// (one side scrapes a rounded string, the other reports exact API bytes — up to
	// ~50 MB apart at GB scale), while the relative budget catches a proportional
	// GiB-vs-GB unit bug (~7.4%) on larger releases. Requiring both keeps a legitimate
	// rounding gap (~40 MB, ~3%) from flapping while still flagging a real unit bug
	// (~100 MB at 1.4 GB, growing with size).
	sizeRelTolerance = 0.02           // relative floor: |h-p|/max(h,p)
	sizeAbsTolerance = 64 << 20       // absolute floor: 64 MiB, covers 1-decimal GB display rounding
	pubDateWindow    = 48 * time.Hour // publish dates within this window agree (some indexers report coarse dates)
	seedersFloor     = 5              // oracle seeder count above which harbrr must also report a positive count
	divergenceSample = 3              // max per-title divergences echoed into a finding's detail
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
	// StrictFields turns on the volatile field-differential checks (seeders,
	// publishDate). Off by default so routine live runs stay green; the stable
	// field checks (size, category, download-url shape) always run.
	StrictFields bool
}

// ParseConfig reads the SMOKE_* environment (via the injected getenv, so tests can
// supply a fake) into a Config. harbrr and Prowlarr are required; the app targets are
// optional (an empty URL means "that app is not configured"). URLs are right-trimmed
// of "/" so a trailing slash never doubles up in an assembled path.
func ParseConfig(getenv func(string) string) (Config, error) {
	get := func(k string) string { return strings.TrimSpace(getenv(k)) }
	cfg := Config{
		HarbrrURL:   strings.TrimRight(get("SMOKE_HARBRR_URL"), "/"),
		HarbrrKey:   get("SMOKE_HARBRR_APIKEY"),
		ProwlarrURL: strings.TrimRight(get("SMOKE_PROWLARR_URL"), "/"),
		ProwlarrKey: get("SMOKE_PROWLARR_APIKEY"),
		SonarrURL:   strings.TrimRight(get("SMOKE_SONARR_URL"), "/"),
		SonarrKey:   get("SMOKE_SONARR_APIKEY"),
		RadarrURL:   strings.TrimRight(get("SMOKE_RADARR_URL"), "/"),
		RadarrKey:   get("SMOKE_RADARR_APIKEY"),
		QuiURL:      strings.TrimRight(get("SMOKE_QUI_URL"), "/"),
		QuiKey:      get("SMOKE_QUI_APIKEY"),
		// Query/FallbackQuery are left empty when unset so chooseQueries can pick a
		// bounded, category-aware default per indexer; an explicit value overrides.
		Query:         get("SMOKE_QUERY"),
		FallbackQuery: get("SMOKE_QUERY_FALLBACK"),
		StrictFields:  truthy(get("SMOKE_STRICT_FIELDS")),
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

// Result is one normalized release for comparison. Title and Size drive the
// result-set differential (DiffPass); the remaining fields feed the field-level
// differential (fieldParity). The volatile fields use pointer/zero sentinels so
// "the oracle didn't populate this" is distinguishable from a real zero and can be
// treated as not-comparable rather than a mismatch.
type Result struct {
	Title       string
	Size        int64
	Categories  []int     // Torznab/Newznab category IDs
	DownloadURL string    // <link>/<enclosure> (harbrr) — expected sealed /dl or magnet, never a raw passkey link
	Seeders     *int      // nil = not reported (volatile; strict only)
	PublishDate time.Time // zero = not reported (volatile; strict only)
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
//
// nocache selects which read path is exercised: true appends harbrr's strict cache
// bypass trigger (nocache=1 — see internal/web/torznabhttp/cachebypass.go), forcing a
// live upstream fetch; false leaves the request on the normal cache-aside path. The
// differential (harbrrParity, the smoke_test.go per-tracker loop) always bypasses —
// Prowlarr, the oracle, is always queried live, so comparing against a frozen harbrr
// cache window can fail a healthy tracker (see issue #164). cacheCheck deliberately
// passes false: it is the dedicated check that still exercises the cache-aside path.
func HarbrrSearch(ctx context.Context, c *http.Client, base, key, slug, query string, nocache bool) ([]Result, int, error) {
	u := harbrrSearchURL(base, key, slug, query, nocache)
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

// harbrrSearchURL builds the harbrr Torznab search URL for a slug+query, optionally
// appending the exact cache-bypass trigger (nocache=1). Split out from HarbrrSearch so
// the URL shape — nocache present vs. absent — is unit-testable without a live server.
func harbrrSearchURL(base, key, slug, query string, nocache bool) string {
	u := fmt.Sprintf("%s/api/indexers/%s/results/torznab/api?t=search&q=%s&apikey=%s",
		base, url.PathEscape(slug), url.QueryEscape(query), url.QueryEscape(key))
	if nocache {
		u += "&nocache=1"
	}
	return u
}

// --- Torznab feed parsing ---------------------------------------------------

type torznabFeed struct {
	Channel struct {
		Items []struct {
			Title      string   `xml:"title"`
			Link       string   `xml:"link"`
			Size       int64    `xml:"size"`
			PubDate    string   `xml:"pubDate"`
			Categories []string `xml:"category"` // parsed leniently: a non-numeric category is skipped, never fails the feed
			Enclosure  struct {
				URL    string `xml:"url,attr"`
				Length int64  `xml:"length,attr"`
			} `xml:"enclosure"`
			// Attrs matches <torznab:attr name= value=> by local name, so the namespace
			// prefix (torznab:) does not need to be declared here.
			Attrs []torznabAttr `xml:"attr"`
		} `xml:"item"`
	} `xml:"channel"`
}

type torznabAttr struct {
	Name  string `xml:"name,attr"`
	Value string `xml:"value,attr"`
}

// ParseTorznab decodes a Torznab RSS feed body into the comparison Results. Beyond
// title + size (size falls back to the enclosure length when <size> is absent) it
// captures the fields the field-level differential compares: categories, the
// download link/enclosure URL, seeders, and the publish date. Parsing is lenient —
// a missing or malformed field is left at its zero value (not-comparable), never an
// error — so the live feed differential degrades gracefully.
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
		dl := it.Link
		if dl == "" {
			dl = it.Enclosure.URL
		}
		r := Result{
			Title:       it.Title,
			Size:        size,
			Categories:  parseCategoryInts(it.Categories),
			DownloadURL: dl,
			PublishDate: parsePubDate(it.PubDate),
		}
		if s, ok := attrInt(it.Attrs, "seeders"); ok {
			r.Seeders = &s
		}
		out = append(out, r)
	}
	return out, nil
}

// parseCategoryInts converts the <category> text nodes to ints, skipping any that
// are not numeric (harbrr emits integer Torznab categories; this only guards a
// malformed live feed from failing the whole parse).
func parseCategoryInts(ss []string) []int {
	out := make([]int, 0, len(ss))
	for _, s := range ss {
		if n, err := strconv.Atoi(strings.TrimSpace(s)); err == nil {
			out = append(out, n)
		}
	}
	return out
}

// attrInt returns the integer value of the first torznab:attr with the given name.
func attrInt(attrs []torznabAttr, name string) (int, bool) {
	for _, a := range attrs {
		if a.Name == name {
			n, err := strconv.Atoi(strings.TrimSpace(a.Value))
			return n, err == nil
		}
	}
	return 0, false
}

// parsePubDate parses a Torznab <pubDate> (RFC1123Z, Jackett's format) or a
// Prowlarr RFC3339 publishDate; an empty or unparseable value yields the zero time
// (not-comparable).
func parsePubDate(s string) time.Time {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}
	}
	for _, layout := range []string{time.RFC1123Z, time.RFC1123, time.RFC3339} {
		if t, err := time.Parse(layout, s); err == nil {
			return t
		}
	}
	return time.Time{}
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
	// Prowlarr's search API has no page cap: a driver with no upstream paging (e.g.
	// a Gazelle browse that returns the whole result set in one response) hands it
	// everything, while harbrr correctly serves a resultCap-sized Torznab page.
	// Comparing the full oracle set against harbrr's one full page false-fails on
	// count (BrokenStones: harbrr 100 vs Prowlarr 696 with identical heads), so
	// clamp the oracle to harbrr's page-1 window.
	note := ""
	if h >= resultCap && p > resultCap {
		note = fmt.Sprintf(" (oracle uncapped at %d, clamped to harbrr's %d-result page window)", p, resultCap)
		prowlarr, p = prowlarr[:resultCap], resultCap
	}
	ratio := float64(min(h, p)) / float64(max(h, p))
	jac := titleJaccard(harbrr, prowlarr)
	if ratio < countRatioMin {
		return false, fmt.Sprintf("count ratio %.2f < %.2f (harbrr %d, Prowlarr %d)%s", ratio, countRatioMin, h, p, note)
	}
	if jac >= titleJaccardMin {
		return true, fmt.Sprintf("count ratio %.2f, title Jaccard %.2f%s", ratio, jac, note)
	}
	// Low title overlap but a full page on both sides: a sort-dependent window of a
	// larger result set (config-driven sort differs between harbrr and Prowlarr).
	// Titles aren't comparable here; accept on strong count parity with a caveat.
	if h >= resultCap && p >= resultCap && ratio >= 0.90 {
		return true, fmt.Sprintf("count parity %.2f at the %d-result page cap; titles incomparable (config-sorted window, Jaccard %.2f)%s", ratio, resultCap, jac, note)
	}
	return false, fmt.Sprintf("title Jaccard %.2f < %.2f (harbrr %d, Prowlarr %d)%s", jac, titleJaccardMin, h, p, note)
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

// --- field-level differential -----------------------------------------------

// FieldDivergence is one normalized field disagreeing between harbrr and the
// Prowlarr oracle for a title present in both result sets. Detail is secret-safe
// by construction (a download-url divergence never echoes the URL, only the offending
// query-param name).
type FieldDivergence struct {
	Title  string
	Field  string
	Detail string
}

// FieldParity is the outcome of the field-level differential across the titles
// present in both result sets: how many titles were comparable, and every field
// that diverged. Windowed reports that comparison was skipped because both sets hit
// the page cap (see fieldParity).
type FieldParity struct {
	Compared    int
	Divergences []FieldDivergence
	Windowed    bool
}

// fieldParity compares normalized fields across the titles present in BOTH result
// sets, matched by normalized title. To avoid ever comparing two different releases
// that happen to share a normalized title, a title is only compared when it is
// unique on both sides. Stable fields (size, category, download-url) are always
// compared; the volatile fields (seeders, publishDate) only when strict is set.
//
// When BOTH sets hit the page cap the results are a sort-dependent window of a larger
// set (the same case DiffPass treats as "titles incomparable"): a shared normalized
// title is then likely a different edition on each side, so field comparison is
// skipped entirely (Windowed) rather than risk a mispaired false divergence. A
// bounded query (see chooseQueries) keeps runs under the cap so fields are compared.
func fieldParity(harbrr, prowlarr []Result, strict bool, harbrrHost string) FieldParity {
	if len(harbrr) >= resultCap && len(prowlarr) >= resultCap {
		return FieldParity{Windowed: true}
	}
	sealed := sealingActive(harbrr, harbrrHost)
	h := uniqueByTitle(harbrr)
	p := uniqueByTitle(prowlarr)
	seen := make(map[string]struct{}, len(p))
	var fp FieldParity
	for _, pr := range prowlarr {
		key := normalizeTitle(pr.Title)
		if key == "" {
			continue
		}
		if _, dup := seen[key]; dup {
			continue
		}
		hu, okh := h[key]
		pu, okp := p[key]
		if !okh || !okp {
			continue
		}
		seen[key] = struct{}{}
		fp.Compared++
		fp.Divergences = append(fp.Divergences, compareFields(hu, pu, strict, sealed)...)
	}
	return fp
}

// sealingActive reports whether any harbrr link points back at harbrr itself — the
// sealed /dl proxy form. The DL rewriter is per-indexer all-or-nothing
// (torznabhttp.NewDLRewriter is nil or rewrites every link), so one sealed link means
// every link in this indexer's feed should be sealed; none sealed means a direct-link
// tracker whose raw links (passkey included) are served as-is by design.
func sealingActive(rs []Result, harbrrHost string) bool {
	if harbrrHost == "" {
		return false
	}
	for _, r := range rs {
		if u, err := url.Parse(r.DownloadURL); err == nil && u.Host == harbrrHost {
			return true
		}
	}
	return false
}

// uniqueByTitle indexes results by normalized title, dropping any title that occurs
// more than once (ambiguous — safer not to compare than to risk pairing the wrong
// two releases).
func uniqueByTitle(rs []Result) map[string]Result {
	counts := make(map[string]int, len(rs))
	idx := make(map[string]Result, len(rs))
	for _, r := range rs {
		k := normalizeTitle(r.Title)
		if k == "" {
			continue
		}
		counts[k]++
		idx[k] = r
	}
	for k, c := range counts {
		if c > 1 {
			delete(idx, k)
		}
	}
	return idx
}

// compareFields runs each per-field check on one matched (harbrr, oracle) pair. The
// download-url leak check only applies when this indexer's links are sealed (see
// downloadURLDivergence) — on a direct-link tracker a raw passkey link is by design.
func compareFields(h, p Result, strict, sealed bool) []FieldDivergence {
	checks := []func(h, p Result) (FieldDivergence, bool){
		sizeDivergence, categoryDivergence,
	}
	if sealed {
		checks = append(checks, downloadURLDivergence)
	}
	if strict {
		checks = append(checks, seedersDivergence, pubDateDivergence)
	}
	var out []FieldDivergence
	for _, check := range checks {
		if d, ok := check(h, p); ok {
			out = append(out, d)
		}
	}
	return out
}

// sizeDivergence flags a size disagreement that exceeds BOTH the relative and the
// absolute budget (see the sizeRelTolerance/sizeAbsTolerance comment). A zero on
// either side means that side did not populate size (not-comparable). Requiring both
// budgets means a legitimate GB display-rounding gap does not flap while a real
// GiB-vs-GB unit bug still trips on any non-trivially-sized release.
func sizeDivergence(h, p Result) (FieldDivergence, bool) {
	if h.Size <= 0 || p.Size <= 0 {
		return FieldDivergence{}, false
	}
	hi, lo := h.Size, p.Size
	if lo > hi {
		hi, lo = lo, hi
	}
	diff := hi - lo
	allowed := max(int64(sizeRelTolerance*float64(hi)), int64(sizeAbsTolerance))
	if diff > allowed {
		return FieldDivergence{
			Title: p.Title, Field: "size",
			Detail: fmt.Sprintf("harbrr=%d oracle=%d (diff %d > allowed %d)", h.Size, p.Size, diff, allowed),
		}, true
	}
	return FieldDivergence{}, false
}

// categoryDivergence flags a release whose major Torznab categories (the thousand-
// bucket: 2040 -> 2000) are disjoint from the oracle's — a gross mis-mapping (movie
// tagged as TV). Sub-category granularity is intentionally ignored so 2040-vs-2000
// does not flap. An empty set on either side is not-comparable.
func categoryDivergence(h, p Result) (FieldDivergence, bool) {
	hm, pm := majorCategories(h.Categories), majorCategories(p.Categories)
	if len(hm) == 0 || len(pm) == 0 {
		return FieldDivergence{}, false
	}
	for c := range hm {
		if _, ok := pm[c]; ok {
			return FieldDivergence{}, false
		}
	}
	return FieldDivergence{
		Title: p.Title, Field: "category",
		Detail: fmt.Sprintf("harbrr major-cats %v disjoint from oracle %v", sortedKeys(hm), sortedKeys(pm)),
	}, true
}

// majorCategories reduces standard Torznab category IDs (1000..99999) to their
// thousand-bucket, ignoring indexer-specific custom categories (>= 100000).
func majorCategories(ids []int) map[int]struct{} {
	m := make(map[int]struct{})
	for _, id := range ids {
		if id >= 1000 && id < 100000 {
			m[(id/1000)*1000] = struct{}{}
		}
	}
	return m
}

// downloadCredentialTokens are the raw-tracker credential query params that must
// never reach a harbrr download link — the sealed /dl proxy keeps them server-side.
// harbrr's own sealed params (apikey/token) are deliberately excluded.
var downloadCredentialTokens = []string{"passkey", "torrent_pass", "authkey", "rsskey", "cf_clearance"}

// downloadURLDivergence flags a harbrr download link that carries a raw tracker
// credential instead of a sealed /dl URL or magnet — both a parity defect and a
// secret leak. It runs only when sealing is active for the indexer (sealingActive):
// feed-side the harness cannot tell "should have been sealed but leaked" from a
// direct-link tracker's correctly-bare passkey link, so the provable defect is a raw
// credential slipping through an ACTIVE rewriter. The oracle side is irrelevant
// (Prowlarr proxies its own links); the detail names only the offending param, never
// the URL.
func downloadURLDivergence(h, _ Result) (FieldDivergence, bool) {
	if h.DownloadURL == "" {
		return FieldDivergence{}, false
	}
	if tok := leakedCredential(h.DownloadURL); tok != "" {
		return FieldDivergence{
			Title: h.Title, Field: "download-url",
			Detail: fmt.Sprintf("harbrr download link carries a raw %q credential (expected a sealed /dl URL or magnet)", tok),
		}, true
	}
	return FieldDivergence{}, false
}

// leakedCredential returns the first raw-credential query param present in a URL, or
// "" if none. Case-insensitive; matches token= or token: forms.
func leakedCredential(rawURL string) string {
	low := strings.ToLower(rawURL)
	for _, tok := range downloadCredentialTokens {
		if strings.Contains(low, tok+"=") || strings.Contains(low, tok+":") {
			return tok
		}
	}
	return ""
}

// seedersDivergence (strict only) flags harbrr reporting no seeders while the oracle
// reports a meaningful count — a broken seeders mapping. Magnitudes move constantly,
// so only presence is compared, and only above seedersFloor so an idle swarm does
// not flap.
func seedersDivergence(h, p Result) (FieldDivergence, bool) {
	if p.Seeders == nil || *p.Seeders < seedersFloor {
		return FieldDivergence{}, false
	}
	if h.Seeders != nil && *h.Seeders > 0 {
		return FieldDivergence{}, false
	}
	harbrrVal := "absent"
	if h.Seeders != nil {
		harbrrVal = "0"
	}
	return FieldDivergence{
		Title: p.Title, Field: "seeders",
		Detail: fmt.Sprintf("oracle reports %d seeders but harbrr reports %s", *p.Seeders, harbrrVal),
	}, true
}

// pubDateDivergence (strict only) flags publish dates more than pubDateWindow apart.
// A zero on either side is not-comparable.
func pubDateDivergence(h, p Result) (FieldDivergence, bool) {
	if h.PublishDate.IsZero() || p.PublishDate.IsZero() {
		return FieldDivergence{}, false
	}
	diff := h.PublishDate.Sub(p.PublishDate)
	if diff < 0 {
		diff = -diff
	}
	if diff > pubDateWindow {
		return FieldDivergence{
			Title: p.Title, Field: "publishDate",
			Detail: fmt.Sprintf("harbrr and oracle publish dates differ by %s (> %s)", diff.Round(time.Hour), pubDateWindow),
		}, true
	}
	return FieldDivergence{}, false
}

// summarizeDivergences renders up to divergenceSample divergences for a finding's
// detail, trailing a "(+N more)" when truncated.
func summarizeDivergences(ds []FieldDivergence) string {
	parts := make([]string, 0, divergenceSample+1)
	for i, d := range ds {
		if i >= divergenceSample {
			parts = append(parts, fmt.Sprintf("(+%d more)", len(ds)-divergenceSample))
			break
		}
		parts = append(parts, fmt.Sprintf("[%s] %q: %s", d.Field, d.Title, d.Detail))
	}
	return strings.Join(parts, "; ")
}

// sortedKeys returns a map's int keys in ascending order (deterministic detail).
func sortedKeys(m map[int]struct{}) []int {
	out := make([]int, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Ints(out)
	return out
}

// --- query selection --------------------------------------------------------

// categoryQuery is a bounded default (primary + fallback) query for one major
// Torznab content category. The queries are deliberately SPECIFIC so both harbrr and
// the oracle return a small, stable, overlapping result set well under the page cap
// (a broad query like "test"/"2024" slams the cap and makes titles incomparable).
// TV uses a single EPISODE, not a whole series, because a series returns hundreds of
// episodes × editions and blows the cap, whereas one episode is naturally bounded.
type categoryQuery struct {
	major             int
	primary, fallback string
}

// categoryQueries is ordered by preference: for an indexer serving several content
// types (a general tracker), the first match wins. A specific film/episode/album/
// title is present-but-bounded on the trackers that carry that content.
var categoryQueries = []categoryQuery{
	{2000, "Oppenheimer 2023", "Dune 2021"},                       // Movies
	{5000, "The Last of Us S01E01", "House of the Dragon S01E01"}, // TV — a single episode, not a series
	{3000, "Radiohead In Rainbows", "Pink Floyd The Wall"},        // Audio — one album
	{7000, "Project Hail Mary", "The Hobbit"},                     // Books — one title
	{4000, "Adobe Photoshop", "Microsoft Office"},                 // PC / apps
	{1000, "God of War", "Elden Ring"},                            // Console / games
}

// genericPrimary/genericFallback are the last-resort defaults when the indexer serves
// no recognized content category or its capabilities could not be fetched. A film
// title is the safest broad-yet-bounded choice (most trackers are movie/TV/general);
// a content-less tracker simply returns 0 and the differential passes as "both empty".
const (
	genericPrimary  = "Oppenheimer 2023"
	genericFallback = "Dune 2021"
)

// chooseQueries picks the (primary, fallback) search queries for one indexer from
// its advertised category IDs. An explicit SMOKE_QUERY always wins (paired with
// SMOKE_QUERY_FALLBACK, or the generic fallback); otherwise the queries are derived
// from the indexer's major categories so the differential compares a bounded,
// content-appropriate result set. It takes raw category IDs (not the appsync type)
// to keep the engine decoupled from the app-sync package.
func chooseQueries(catIDs []int, cfg Config) (primary, fallback string) {
	if cfg.Query != "" {
		fb := cfg.FallbackQuery
		if fb == "" {
			fb = genericFallback
		}
		return cfg.Query, fb
	}
	majors := majorCategories(catIDs)
	for _, cq := range categoryQueries {
		if _, ok := majors[cq.major]; ok {
			return cq.primary, cq.fallback
		}
	}
	return genericPrimary, genericFallback
}

// truthy reports whether an env value is an affirmative flag.
func truthy(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
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
