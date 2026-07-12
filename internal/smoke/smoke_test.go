//go:build smoke

// LIVE smoke + Prowlarr differential. Manual only; never in CI.
//
// Discovers the enabled indexers already configured in the running harbrr
// daemon, matches each against Prowlarr, and asserts the two agree within a
// tolerance (live data is non-deterministic). Sequential with gentle delays;
// backs off on rate-limit. Captures secret-free evidence.
//
// The pure parity engine (Config/ParseConfig, Result/DiffPass, the
// search/parse, and evidence helpers) lives in engine.go; this file is only
// the *testing.T front-end.
//
// Required env (see docs/smoke-setup.md):
//
//	SMOKE_HARBRR_URL, SMOKE_HARBRR_APIKEY
//	SMOKE_PROWLARR_URL, SMOKE_PROWLARR_APIKEY
//	SMOKE_QUERY (optional, default "test"), SMOKE_QUERY_FALLBACK (default "2024")
//	SMOKE_GRAB=1 (optional) — also resolve the first release's link to a real
//	   .torrent/magnet (the qBittorrent push + seeding stays a manual, no-H&R step).
package smoke

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	apphttp "github.com/autobrr/harbrr/internal/http"
)

func TestSmoke(t *testing.T) {
	cfg, err := ParseConfig(os.Getenv)
	if err != nil {
		t.Fatalf("smoke: %v", err)
	}
	c := &http.Client{Timeout: httpTimeout}

	indexers, err := listHarbrrIndexers(context.Background(), c, cfg)
	if err != nil {
		t.Fatalf("smoke: list harbrr indexers: %s", apphttp.RedactError(err))
	}
	enabled := make([]harbrrIndexer, 0, len(indexers))
	for _, ix := range indexers {
		if ix.Enabled {
			enabled = append(enabled, ix)
		}
	}
	if len(enabled) == 0 {
		t.Skip("no enabled indexers configured in harbrr")
	}

	for i, ix := range enabled {
		t.Run(ix.Slug, func(t *testing.T) {
			// Category-aware bounded default query (a specific film/episode/album/title)
			// so the differential compares a small, stable, overlapping set instead of
			// slamming the page cap. An explicit SMOKE_QUERY overrides.
			cats, _ := harbrrCategories(context.Background(), c, cfg, ix.Slug)
			primary, fallback := chooseQueries(categoryIDsOf(cats), cfg)
			q := primary
			harbrr, skipped := harbrrSearch(t, c, cfg, ix.Slug, q)
			if skipped {
				return
			}
			if len(harbrr) == 0 {
				q = fallback
				harbrr, skipped = harbrrSearch(t, c, cfg, ix.Slug, q)
				if skipped {
					return
				}
			}

			time.Sleep(betweenCallsDelay)
			prowlarrID, ok, perr := ProwlarrIndexerID(context.Background(), c, cfg.ProwlarrURL, cfg.ProwlarrKey, ix.Name, ix.Slug)
			if perr != nil {
				t.Skipf("%s: Prowlarr oracle unavailable (%s); skipping differential", ix.Slug, apphttp.RedactError(perr))
				return
			}
			if !ok {
				t.Skipf("%s: no matching Prowlarr indexer; skipping differential", ix.Slug)
				return
			}
			prowlarr, pStatus, perr := ProwlarrSearch(context.Background(), c, cfg.ProwlarrURL, cfg.ProwlarrKey, prowlarrID, q)
			switch {
			case perr != nil:
				t.Skipf("%s: Prowlarr oracle unavailable (%s); skipping differential", ix.Slug, apphttp.RedactError(perr))
				return
			case pStatus == http.StatusTooManyRequests || pStatus == http.StatusServiceUnavailable:
				t.Skipf("%s: Prowlarr rate-limited (HTTP %d); backing off", ix.Slug, pStatus)
				return
			case pStatus != http.StatusOK:
				t.Skipf("%s: Prowlarr oracle HTTP %d; skipping differential", ix.Slug, pStatus)
				return
			}

			pass, notes := DiffPass(harbrr, prowlarr)
			fp := fieldParity(harbrr, prowlarr, cfg.StrictFields)
			if len(fp.Divergences) > 0 {
				// Only the count reaches the evidence Notes (titles stay out of persisted
				// evidence); the per-title detail is logged to the operator console, scrubbed
				// in case a tracker echoed a credential into a release title.
				notes += fmt.Sprintf(" | field divergences: %d across %d shared titles", len(fp.Divergences), fp.Compared)
				t.Logf("%s: field divergences: %s", ix.Slug, scrubSecretValues(summarizeDivergences(fp.Divergences)))
			}
			rec := EvidenceRecord{
				Tracker:              ix.Slug,
				Query:                q,
				HarbrrCount:          len(harbrr),
				ProwlarrCount:        len(prowlarr),
				HarbrrTitles:         firstTitles(harbrr, 5),
				ProwlarrTitles:       firstTitles(prowlarr, 5),
				DownloadLinksPresent: harbrrHasDownloadLinks(t, c, cfg, ix.Slug, q),
				Pass:                 pass,
				Notes:                notes,
			}
			if os.Getenv("SMOKE_GRAB") == "1" {
				rec.Grab = grabResolve(t, c, cfg, ix.Slug, q)
			}
			writeEvidence(t, rec)
			t.Logf("%s: harbrr=%d prowlarr=%d pass=%v (%s)", ix.Slug, len(harbrr), len(prowlarr), pass, notes)
			if !pass {
				t.Errorf("differential FAILED for %s: %s", ix.Slug, notes)
			}
			// Any field divergence fails the run, matching the CLI report path
			// (fieldParityFinding). Only the STABLE fields (size, category, download-url)
			// populate divergences by default; the volatile seeders/publishDate checks are
			// added to fp.Divergences only under SMOKE_STRICT_FIELDS, so a routine run still
			// stays green on volatile data.
			if len(fp.Divergences) > 0 {
				t.Errorf("field parity FAILED for %s: %s", ix.Slug, scrubSecretValues(summarizeDivergences(fp.Divergences)))
			}
			if i < len(enabled)-1 {
				time.Sleep(betweenTrackerDelay)
			}
		})
	}

	// The differential above always bypasses the cache (nocache=1, see harbrrSearch),
	// so it is no longer this suite's coverage of the cache-aside read path. Run the
	// same single-tracker cached-path check RunSuite uses (cacheCheck, package-shared
	// with checks.go): one designated tracker, not per-tracker, keeps this cheap.
	t.Run("cache", func(t *testing.T) {
		f := cacheCheck(context.Background(), c, cfg, enabled[0].Slug)
		switch f.Status {
		case StatusSkip:
			t.Skip(f.Detail)
		case StatusFail:
			t.Errorf("cached-path check FAILED for %s: %s", f.Indexer, f.Detail)
		default:
			t.Logf("%s: %s", f.Indexer, f.Detail)
		}
	})
}

// harbrrSearch queries harbrr's Torznab feed for the differential, bypassing the
// search cache (nocache=1) so it compares against Prowlarr's always-live query rather
// than a possibly-frozen cache window (issue #164). Returns (results, skipped);
// skipped is true on a rate-limit/anti-bot signal (the test t.Skips rather than
// hammering).
func harbrrSearch(t *testing.T, c *http.Client, cfg Config, slug, query string) ([]Result, bool) {
	t.Helper()
	res, status, err := HarbrrSearch(context.Background(), c, cfg.HarbrrURL, cfg.HarbrrKey, slug, query, true)
	if status == http.StatusTooManyRequests || status == http.StatusServiceUnavailable {
		t.Skipf("%s: harbrr feed rate-limited (HTTP %d); backing off", slug, status)
		return nil, true
	}
	if err != nil {
		t.Fatalf("%s: harbrr feed: %s", slug, apphttp.RedactError(err))
	}
	if status != http.StatusOK {
		t.Fatalf("%s: harbrr feed HTTP %d", slug, status)
	}
	return res, false
}

// grabResolve fetches the first served release's download link and confirms a real
// .torrent (bencode) or a magnet — proving the grab path resolves end to end. It does
// NOT push to qBittorrent; the no-hit-and-run seeding step stays a manual confirmation
// (see README). Gated by SMOKE_GRAB since it pulls a real .torrent from the tracker.
func grabResolve(t *testing.T, c *http.Client, cfg Config, slug, query string) string {
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
		t.Fatalf("grab %s: %s", slug, apphttp.RedactError(err))
	}
	if status != http.StatusOK {
		return fmt.Sprintf("download HTTP %d", status)
	}
	if len(body) > 0 && body[0] == 'd' { // a bencoded torrent dict starts with 'd'
		return "torrent"
	}
	return "not a torrent/magnet"
}

// firstDownloadLink returns the first feed item's link/enclosure URL.
func firstDownloadLink(t *testing.T, c *http.Client, cfg Config, slug, query string) string {
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
func harbrrHasDownloadLinks(t *testing.T, c *http.Client, cfg Config, slug, query string) bool {
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
func harbrrFeedBody(t *testing.T, c *http.Client, cfg Config, slug, query string) []byte {
	t.Helper()
	u := fmt.Sprintf("%s/api/indexers/%s/results/torznab/api?t=search&q=%s&apikey=%s",
		cfg.HarbrrURL, url.PathEscape(slug), url.QueryEscape(query), url.QueryEscape(cfg.HarbrrKey))
	body, status, err := httpGet(context.Background(), c, u, nil)
	if err != nil || status != http.StatusOK {
		return nil
	}
	return body
}

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
