package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/normalizer"
	"github.com/autobrr/harbrr/internal/web/torznab"
)

// searchResponse is the JSON body of GET /api/indexers/{slug}/search.
type searchResponse struct {
	Results []*normalizer.Release `json:"results"`
}

// searchIndexer runs a JSON search against a configured indexer and returns the
// same releases the Torznab feed serves for the same query — it calls the shared
// read pipeline (torznab.SearchReleases), so the result set is identical to the
// feed's (parity). For a resolver-needing indexer each download link is sealed
// behind the /dl proxy, so a passkey never reaches the response, exactly as the
// feed does. Query params are the Torznab set (q, cat, the external ids, season/ep,
// year, the music/book fields, limit, offset). An unknown or disabled slug is a 404;
// a search failure is a redacted 500.
func (rt *router) searchIndexer(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")
	idx, ok := rt.registry.Indexer(r.Context(), slug)
	if !ok {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	res, err := torznab.SearchReleases(r.Context(), idx, r.URL.Query())
	if err != nil {
		rt.writeServiceError(w, "search indexer", err)
		return
	}
	writeJSON(w, http.StatusOK, searchResponse{Results: rt.resolveSearchLinks(r, idx, res.Releases)})
}

// resolveSearchLinks returns copies of the releases with download links made safe to
// serve: a resolver-needing indexer's link is sealed behind the /dl proxy (the
// passkey stays inside harbrr), a direct link/magnet is served as-is, and — when the
// proxy is disabled but the indexer needs resolution — the link is withheld rather
// than served in the clear. Production always wires the keyring, so the withhold case
// is a defensive guard. It copies each release so the engine's results are not
// mutated.
func (rt *router) resolveSearchLinks(r *http.Request, idx torznab.Indexer, releases []*normalizer.Release) []*normalizer.Release {
	rw := torznab.NewDLRewriter(rt.dlToken, idx, torznab.DLBaseURL(r, rt.basePath, idx.Info().ID), r.Header.Get("X-API-Key"))
	withhold := rw == nil && torznab.NeedsDLProxy(idx)
	out := make([]*normalizer.Release, len(releases))
	for i, rel := range releases {
		cp := *rel
		switch {
		case rw != nil:
			acq := cp.Link
			if acq == "" {
				acq = cp.Magnet
			}
			if link, _, ok := rw(acq); ok {
				cp.Link = link
			}
		case withhold:
			cp.Link, cp.Magnet = "", ""
		}
		out[i] = &cp
	}
	return out
}
