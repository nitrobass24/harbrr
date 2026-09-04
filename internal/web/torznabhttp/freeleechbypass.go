package torznabhttp

import (
	"net/http"

	"github.com/autobrr/harbrr/internal/indexer/core"
	"github.com/autobrr/harbrr/internal/indexer/grab"
)

// withFreeleechBypass wraps a handler so every request it serves carries the
// freeleech-bypass marker (core.WithFreeleechBypass). The bypass feed routes are
// registered through it, so the same serve/caps code path drives both variants —
// only the marker differs.
func withFreeleechBypass(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		next(w, r.WithContext(core.WithFreeleechBypass(r.Context())))
	}
}

// FeedURL builds the externally-visible Torznab results-feed URL for an indexer (no
// apikey appended). bypass selects the freeleech-bypass /full variant registered
// above — the URL harbrr hands cross-seed consumers that must see the full catalog.
// It reuses grab's origin/base-path derivation, the same one behind grab.DLBaseURL,
// so the feed URL and the /dl URLs it emits cannot disagree about where harbrr is.
func FeedURL(r *http.Request, cfg grab.URLConfig, indexerID string, bypass bool) string {
	u := grab.ExternalIndexerBase(r, cfg, indexerID) + "/results/torznab"
	if bypass {
		u += "/full"
	}
	return u
}
