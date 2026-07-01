package smoke

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/autobrr/harbrr/internal/appsync"
	"github.com/autobrr/harbrr/internal/domain"
	apphttp "github.com/autobrr/harbrr/internal/http"
)

// --- parity -----------------------------------------------------------------

// parityCheck runs the Prowlarr differential for one indexer: search harbrr, resolve
// the matching Prowlarr indexer by name, search Prowlarr, and DiffPass the two. An
// indexer absent from Prowlarr is not-comparable (StatusNA), an oracle hiccup is a
// SKIP; only a genuine result-set divergence is a FAIL.
func parityCheck(ctx context.Context, c *http.Client, cfg Config, ix harbrrIndexer) Finding {
	f := Finding{Indexer: ix.Slug, Check: CheckParity}
	harbrr, query, skip := harbrrParity(ctx, c, cfg, ix.Slug)
	if skip != "" {
		return skipFinding(f, skip)
	}
	id, comparable, skip := prowlarrLookup(ctx, c, cfg, ix.Name, ix.Slug)
	if skip != "" {
		return skipFinding(f, skip)
	}
	if !comparable {
		f.Status, f.Detail = StatusNA, fmt.Sprintf("no Prowlarr indexer matching %q (%s) (not comparable)", ix.Name, ix.Slug)
		return f
	}
	time.Sleep(betweenCallsDelay)
	prowlarr, skip := prowlarrResults(ctx, c, cfg, id, query)
	if skip != "" {
		return skipFinding(f, skip)
	}
	pass, notes := DiffPass(harbrr, prowlarr)
	f.Status = StatusFail
	if pass {
		f.Status = StatusPass
	}
	f.Detail = fmt.Sprintf("q=%q harbrr=%d prowlarr=%d: %s", query, len(harbrr), len(prowlarr), notes)
	if !pass {
		// A divergence is the whole point of the report — carry a few sample titles from
		// each side so the operator can eyeball what differs (scrubbed by the renderer).
		f.Detail += fmt.Sprintf(" | harbrr sample: %v | prowlarr sample: %v",
			firstTitles(harbrr, 3), firstTitles(prowlarr, 3))
	}
	return f
}

// harbrrParity searches harbrr, falling back to the secondary query when the primary
// returns nothing. It returns the results, the query that produced them, and a non-empty
// skip reason on a rate-limit/transport/non-200.
func harbrrParity(ctx context.Context, c *http.Client, cfg Config, slug string) ([]Result, string, string) {
	res, status, err := HarbrrSearch(ctx, c, cfg.HarbrrURL, cfg.HarbrrKey, slug, cfg.Query)
	if err != nil {
		return nil, "", apphttp.RedactError(err)
	}
	if status == http.StatusTooManyRequests || status == http.StatusServiceUnavailable {
		return nil, "", fmt.Sprintf("harbrr feed rate-limited (HTTP %d)", status)
	}
	if status != http.StatusOK {
		return nil, "", fmt.Sprintf("harbrr feed HTTP %d", status)
	}
	query := cfg.Query
	if len(res) == 0 {
		if res2, s2, err2 := HarbrrSearch(ctx, c, cfg.HarbrrURL, cfg.HarbrrKey, slug, cfg.FallbackQuery); err2 == nil && s2 == http.StatusOK {
			res, query = res2, cfg.FallbackQuery
		}
	}
	return res, query, ""
}

// prowlarrLookup resolves the Prowlarr indexer id for a harbrr indexer by its display
// name and slug. A transport error is a skip; a clean "not found" yields comparable=false
// (the caller marks it not-comparable).
func prowlarrLookup(ctx context.Context, c *http.Client, cfg Config, name, slug string) (id int, comparable bool, skip string) {
	id, ok, err := ProwlarrIndexerID(ctx, c, cfg.ProwlarrURL, cfg.ProwlarrKey, name, slug)
	if err != nil {
		return 0, false, "Prowlarr oracle unavailable: " + apphttp.RedactError(err)
	}
	return id, ok, ""
}

// prowlarrResults searches Prowlarr for one indexer id; any oracle error/non-200 is a
// skip (oracle-side, never a harbrr failure).
func prowlarrResults(ctx context.Context, c *http.Client, cfg Config, id int, query string) ([]Result, string) {
	res, status, err := ProwlarrSearch(ctx, c, cfg.ProwlarrURL, cfg.ProwlarrKey, id, query)
	if err != nil {
		return nil, "Prowlarr oracle unavailable: " + apphttp.RedactError(err)
	}
	if status != http.StatusOK {
		return nil, fmt.Sprintf("Prowlarr oracle HTTP %d", status)
	}
	return res, ""
}

// --- app-sync ---------------------------------------------------------------

// appTarget is one configured app under test: its kind (for the category filter), a
// short label, the appsync driver used to list its indexers, and that indexer list cached
// once per run (remotes / listErr) so the per-indexer app-sync checks don't re-fetch it.
type appTarget struct {
	kind    string
	label   string
	target  appsync.Target
	remotes []appsync.RemoteIndexer
	listErr error
}

// loadRemotes fetches each app's remote indexer list once, caching it on the appTarget so
// appSyncCheck reads the cache instead of calling List(ctx) per harbrr indexer (which would
// be indexers×apps redundant round-trips to the *arr/qui APIs).
func loadRemotes(ctx context.Context, apps []appTarget) {
	for i := range apps {
		apps[i].remotes, apps[i].listErr = apps[i].target.List(ctx)
	}
}

// configuredApps builds a driver for each app whose URL+key is set (an empty URL means
// "not configured"); qui/Sonarr/Radarr only.
func configuredApps(cfg Config) []appTarget {
	client := &http.Client{Timeout: httpTimeout}
	var apps []appTarget
	if cfg.SonarrURL != "" && cfg.SonarrKey != "" {
		apps = append(apps, appTarget{kind: domain.AppKindSonarr, label: "sonarr", target: appsync.NewSonarr(cfg.SonarrURL, cfg.SonarrKey, client)})
	}
	if cfg.RadarrURL != "" && cfg.RadarrKey != "" {
		apps = append(apps, appTarget{kind: domain.AppKindRadarr, label: "radarr", target: appsync.NewRadarr(cfg.RadarrURL, cfg.RadarrKey, client)})
	}
	if cfg.QuiURL != "" && cfg.QuiKey != "" {
		apps = append(apps, appTarget{kind: domain.AppKindQui, label: "qui", target: appsync.NewQui(cfg.QuiURL, cfg.QuiKey, client)})
	}
	return apps
}

// appSyncChecks runs the app-sync assertions for one indexer across every configured
// app. It fetches the indexer's categories once, then delegates per app.
func appSyncChecks(ctx context.Context, c *http.Client, cfg Config, apps []appTarget, ix harbrrIndexer) []Finding {
	if len(apps) == 0 {
		return nil
	}
	cats, err := harbrrCategories(ctx, c, cfg, ix.Slug)
	if err != nil {
		return []Finding{skipFinding(Finding{Indexer: ix.Slug, Check: CheckAppSync}, apphttp.RedactError(err))}
	}
	out := make([]Finding, 0, len(apps)+1)
	for _, app := range apps {
		out = append(out, appSyncCheck(ctx, c, cfg, app, ix, cats)...)
	}
	return out
}

// appSyncCheck asserts the content-category filter holds for one app: the indexer is
// present in the app iff IndexerServesApp says it should be. When present it also
// validates the pushed feed URL shape and that the feed answers t=caps with 200; qui
// additionally yields a separate freeleech-bypass (/full) finding.
func appSyncCheck(ctx context.Context, c *http.Client, cfg Config, app appTarget, ix harbrrIndexer, cats []appsync.Category) []Finding {
	check := app.label + " " + CheckAppSync
	if app.listErr != nil {
		return []Finding{skipFinding(Finding{Indexer: ix.Slug, Check: check}, apphttp.RedactError(app.listErr))}
	}
	remotes := app.remotes
	serves := appsync.IndexerServesApp(app.kind, cats)
	matched, present := findManaged(remotes, ix.Slug)
	switch {
	case serves != present:
		return []Finding{{Indexer: ix.Slug, Check: check, Status: StatusFail, Detail: fmt.Sprintf(
			"category filter mismatch: should-serve-%s=%v but present-in-%s=%v", app.label, serves, app.label, present)}}
	case !present:
		return []Finding{{Indexer: ix.Slug, Check: check, Status: StatusPass, Detail: fmt.Sprintf(
			"correctly absent (does not serve %s)", app.label)}}
	default:
		return presentFindings(ctx, c, cfg, app, ix.Slug, check, matched)
	}
}

// presentFindings validates a present, harbrr-managed feed: its URL shape, its live
// t=caps response, and (qui only) the /full freeleech-bypass suffix.
func presentFindings(ctx context.Context, c *http.Client, cfg Config, app appTarget, slug, check string, matched appsync.RemoteIndexer) []Finding {
	issues := feedURLIssues(slug, matched.FeedURL)
	if status, err := feedCapsStatus(ctx, c, cfg, matched.FeedURL); err != nil {
		issues = append(issues, "caps probe: "+apphttp.RedactError(err))
	} else if status != http.StatusOK {
		issues = append(issues, fmt.Sprintf("caps probe HTTP %d", status))
	}
	out := []Finding{appSyncResult(slug, check, matched.FeedURL, issues)}
	if app.kind == domain.AppKindQui {
		out = append(out, flBypassFinding(slug, matched.FeedURL))
	}
	return out
}

// appSyncResult turns the collected feed-URL/caps issues into a PASS/FAIL finding.
func appSyncResult(slug, check, feed string, issues []string) Finding {
	if len(issues) == 0 {
		return Finding{Indexer: slug, Check: check, Status: StatusPass, Detail: fmt.Sprintf(
			"present, feed %s, caps 200", apphttp.RedactURL(feed))}
	}
	return Finding{Indexer: slug, Check: check, Status: StatusFail, Detail: strings.Join(issues, "; ")}
}

// feedURLIssues checks the pushed feed URL is the new per-slug Torznab path and not the
// legacy /api/v2.0/ shape.
func feedURLIssues(slug, feed string) []string {
	var issues []string
	want := "/api/indexers/" + slug + "/results/torznab"
	if !strings.Contains(feed, want) {
		issues = append(issues, "feed URL missing "+want+" (got "+apphttp.RedactURL(feed)+")")
	}
	if strings.Contains(feed, "/api/v2.0/") {
		issues = append(issues, "feed URL uses the legacy /api/v2.0/ path")
	}
	return issues
}

// flBypassFinding records whether qui's synced feed is the /full freeleech-bypass
// variant (its own check so the report calls it out distinctly).
func flBypassFinding(slug, feed string) Finding {
	f := Finding{Indexer: slug, Check: CheckFLBypass}
	if strings.HasSuffix(feed, "/results/torznab/full") {
		f.Status, f.Detail = StatusPass, "qui feed uses the /full freeleech-bypass variant"
		return f
	}
	f.Status = StatusFail
	f.Detail = "qui feed is not the /full variant: " + apphttp.RedactURL(feed)
	return f
}

// feedCapsStatus probes a synced feed URL with t=caps + the harbrr key, returning the
// HTTP status (200 confirms harbrr serves the app-facing feed).
func feedCapsStatus(ctx context.Context, c *http.Client, cfg Config, feed string) (int, error) {
	u := feed + "?t=caps&apikey=" + url.QueryEscape(cfg.HarbrrKey)
	_, status, err := httpGet(ctx, c, u, nil)
	return status, err
}

// findManaged returns the harbrr-managed remote indexer for a slug, if the app has one.
func findManaged(remotes []appsync.RemoteIndexer, slug string) (appsync.RemoteIndexer, bool) {
	for _, r := range remotes {
		if r.ManagedBySlug == slug {
			return r, true
		}
	}
	return appsync.RemoteIndexer{}, false
}

// --- cache ------------------------------------------------------------------

// cacheCheck confirms the search-results cache serves a repeated query from cache: it
// reads the baseline trackerHitsSaved, runs two identical harbrr searches, and asserts
// the counter incremented. A disabled cache is a SKIP.
func cacheCheck(ctx context.Context, c *http.Client, cfg Config, slug string) Finding {
	f := Finding{Indexer: slug, Check: CheckCache}
	before, enabled, err := cacheTrackerHits(ctx, c, cfg)
	if err != nil {
		return skipFinding(f, apphttp.RedactError(err))
	}
	if !enabled {
		return skipFinding(f, "cache disabled")
	}
	for i := 0; i < 2; i++ {
		if _, _, serr := HarbrrSearch(ctx, c, cfg.HarbrrURL, cfg.HarbrrKey, slug, cfg.Query); serr != nil {
			return skipFinding(f, apphttp.RedactError(serr))
		}
		time.Sleep(betweenCallsDelay)
	}
	after, _, err := cacheTrackerHits(ctx, c, cfg)
	if err != nil {
		return skipFinding(f, apphttp.RedactError(err))
	}
	if after > before {
		f.Status, f.Detail = StatusPass, fmt.Sprintf("trackerHitsSaved %d -> %d (repeat search served from cache)", before, after)
		return f
	}
	f.Status, f.Detail = StatusFail, fmt.Sprintf("trackerHitsSaved did not increment (%d -> %d)", before, after)
	return f
}

// cacheTrackerHits reads the cache stats' trackerHitsSaved counter and whether caching
// is enabled.
func cacheTrackerHits(ctx context.Context, c *http.Client, cfg Config) (int64, bool, error) {
	body, status, err := harbrrGet(ctx, c, cfg, "/api/cache/stats")
	if err != nil {
		return 0, false, err
	}
	if status != http.StatusOK {
		return 0, false, fmt.Errorf("harbrr GET /api/cache/stats: HTTP %d", status)
	}
	var s struct {
		Enabled          bool  `json:"enabled"`
		TrackerHitsSaved int64 `json:"trackerHitsSaved"`
	}
	if err := json.Unmarshal(body, &s); err != nil {
		return 0, false, fmt.Errorf("parse cache stats: %w", err)
	}
	return s.TrackerHitsSaved, s.Enabled, nil
}

// skipFinding sets a finding to SKIP with a detail.
func skipFinding(f Finding, detail string) Finding {
	f.Status, f.Detail = StatusSkip, detail
	return f
}
