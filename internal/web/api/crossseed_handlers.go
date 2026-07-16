package api

import (
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/autobrr/harbrr/internal/web/torznabhttp"
)

// apiKeyPlaceholder is the literal a cross-seed snippet carries in place of a real harbrr
// API key. harbrr stores keys only as hashes, so it cannot echo a usable key back; the
// operator pastes one of their own minted keys over this token. (This is not a secret.)
const apiKeyPlaceholder = "<YOUR_HARBRR_API_KEY>" //nolint:gosec // G101: a placeholder token, not a credential.

// crossSeedSnippetResponse is the copy-paste config for one indexer's cross-seed v6
// torznab entry. cross-seed v6 has no indexer API (file config + restart), so harbrr
// emits the snippet rather than pushing it. The URL is the freeleech-bypass /full
// variant — cross-seed must see the full catalog, not the *arr freeleech-only view.
type crossSeedSnippetResponse struct {
	Indexer  string `json:"indexer"`
	FeedURL  string `json:"feedUrl"`
	ConfigJs string `json:"configJs"`
}

// crossSeedSnippet returns the cross-seed v6 config.js torznab entry for one indexer.
func (rt *router) crossSeedSnippet(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")
	// Resolve the indexer so an unknown slug 404s rather than emitting a dead URL.
	if _, _, err := rt.registry.Get(r.Context(), slug); err != nil {
		rt.writeServiceError(w, "cross-seed snippet", err)
		return
	}
	feedURL := torznabhttp.FeedURL(r, rt.urlCfg, slug, true)
	writeJSON(w, http.StatusOK, crossSeedSnippetResponse{
		Indexer:  slug,
		FeedURL:  feedURL,
		ConfigJs: crossSeedConfigJs(feedURL),
	})
}

// crossSeedConfigJs renders the torznab array entry for a config.js, with the apikey
// placeholder appended.
func crossSeedConfigJs(feedURL string) string {
	return fmt.Sprintf("torznab: [\n  \"%s?apikey=%s\",\n],", feedURL, apiKeyPlaceholder)
}
