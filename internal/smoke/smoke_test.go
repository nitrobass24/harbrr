//go:build smoke

// LIVE smoke + Prowlarr differential, as a `go test` front-end. Manual only; never in
// CI. It is a thin wrapper over RunSuite (run.go) — the same suite `harbrr smoke` runs —
// so both entry points execute identical checks against the live stack; only the
// reporting differs (findings as subtests here, a markdown report there).
//
// Required env (see docs/smoke-setup.md):
//
//	SMOKE_HARBRR_URL, SMOKE_HARBRR_APIKEY
//	SMOKE_PROWLARR_URL, SMOKE_PROWLARR_APIKEY
//	SMOKE_QUERY (optional; default is a per-tracker category-aware query),
//	SMOKE_QUERY_FALLBACK (optional)
//	SMOKE_GRAB=1 (optional) — also resolve the first release's link to a real
//	   .torrent/magnet (the qBittorrent push + seeding stays a manual, no-H&R step).
package smoke

import (
	"context"
	"os"
	"testing"

	apphttp "github.com/autobrr/harbrr/internal/http"
)

func TestSmoke(t *testing.T) {
	cfg, err := ParseConfig(os.Getenv)
	if err != nil {
		t.Fatalf("smoke: %v", err)
	}
	rep, err := RunSuite(context.Background(), cfg)
	if err != nil {
		t.Fatalf("smoke: %s", apphttp.RedactError(err))
	}
	if len(rep.Findings) == 0 {
		t.Skip("no enabled indexers configured in harbrr")
	}
	for _, f := range rep.Findings {
		t.Run(f.Indexer+"/"+f.Check, func(t *testing.T) {
			switch f.Status {
			case StatusFail:
				t.Errorf("FAILED: %s", f.Detail)
			case StatusSkip:
				t.Skip(f.Detail)
			default:
				t.Log(f.Detail)
			}
		})
	}
	t.Log(rep.Summary())
}
