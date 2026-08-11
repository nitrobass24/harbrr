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
	"fmt"
	"net/http"
	"os"
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
			fp := fieldParity(harbrr, prowlarr, cfg.StrictFields, hostOf(cfg.HarbrrURL))
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
				DownloadLinksPresent: hasDownloadLinks(t, c, cfg, ix.Slug, q),
				Pass:                 pass,
				Notes:                notes,
			}
			if cfg.Grab {
				rec.Grab = grabResolveOrFatal(t, c, cfg, ix.Slug, q)
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
			// A recorded grab result is binding: a 100% search differential means nothing
			// if the download link 500s (issue #429). rec.Grab is "" when SMOKE_GRAB is
			// unset, which GrabSucceeded treats as "not attempted", not a failure.
			if !GrabSucceeded(rec.Grab) {
				t.Errorf("grab FAILED for %s: %s", ix.Slug, rec.Grab)
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
		cats, _ := harbrrCategories(context.Background(), c, cfg, enabled[0].Slug)
		f := cacheCheck(context.Background(), c, cfg, enabled[0].Slug, categoryIDsOf(cats))
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

// grabResolveOrFatal is the *testing.T front-end for the engine's grabResolve (engine.go,
// untagged and shared with the CLI's grabCheck): it resolves the first served release's
// download link and names the payload, fatalling on the transport/feed error the engine
// now returns instead of swallowing. It does NOT push to qBittorrent; the no-hit-and-run
// seeding step stays a manual confirmation (see README).
func grabResolveOrFatal(t *testing.T, c *http.Client, cfg Config, slug, query string) string {
	t.Helper()
	res, err := grabResolve(context.Background(), c, cfg, slug, query)
	if err != nil {
		t.Fatalf("grab %s: %s", slug, apphttp.RedactError(err))
	}
	return res
}

// hasDownloadLinks is the *testing.T front-end for harbrrHasDownloadLinks: a feed error
// is not a failure of this probe (it records "no grabbable link" as before), only a log
// line so the operator sees why.
func hasDownloadLinks(t *testing.T, c *http.Client, cfg Config, slug, query string) bool {
	t.Helper()
	ok, err := harbrrHasDownloadLinks(context.Background(), c, cfg, slug, query)
	if err != nil {
		t.Logf("%s: download-link probe: %s", slug, apphttp.RedactError(err))
	}
	return ok
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
