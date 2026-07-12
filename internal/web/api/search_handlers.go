package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/normalizer"
	"github.com/autobrr/harbrr/internal/web/torznabhttp"
)

// searchResponse is the JSON body of GET /api/indexers/{slug}/search. It mirrors qui's
// paged-list envelope: the results for this page plus the paging metadata, so a client
// can page without re-deriving counts. Total is the full match count before the page
// slice; HasMore reports whether results beyond this page exist; Limit/Offset are the
// resolved window. The fields are additive — a pre-reshape consumer reading only
// `results` is unaffected.
type searchResponse struct {
	Results []*normalizer.Release `json:"results"`
	Total   int                   `json:"total"`
	HasMore bool                  `json:"hasMore"`
	Limit   int                   `json:"limit"`
	Offset  int                   `json:"offset"`
}

// newSearchResponse builds the qui-shaped envelope from the shared pipeline's result
// and the page's resolved (link-sealed) releases. HasMore is computed from the pipeline
// page length and Total (the pre-slice match count), so it is correct at every boundary
// — including an offset at or past Total (empty page, no more) and a partial last page.
func newSearchResponse(res torznabhttp.SearchResult, results []*normalizer.Release) searchResponse {
	return searchResponse{
		Results: results,
		Total:   res.Total,
		HasMore: res.Offset+len(res.Releases) < res.Total,
		Limit:   res.Limit,
		Offset:  res.Offset,
	}
}

// searchIndexer runs a JSON search against a configured indexer and returns the
// same releases the Torznab feed serves for the same query — it calls the shared
// read pipeline (torznabhttp.SearchReleases), so the result set is identical to the
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
	res, err := torznabhttp.SearchReleases(r.Context(), idx, r.URL.Query())
	if err != nil {
		rt.writeServiceError(w, "search indexer", err)
		return
	}
	writeJSON(w, http.StatusOK, newSearchResponse(res, rt.resolveSearchLinks(r, idx, res.Releases)))
}

// resolveSearchLinks returns copies of the releases with download links made safe to
// serve: a resolver-needing indexer's link is sealed behind the session-authed
// management download route (the passkey stays inside harbrr), a direct link/magnet is
// served as-is, and — when the proxy is disabled but the indexer needs resolution — the
// link is withheld rather than served in the clear. Production always wires the keyring,
// so the withhold case is a defensive guard. It copies each release so the engine's
// results are not mutated.
//
// The JSON search API is the web-UI surface: it authenticates by session cookie (never
// X-API-Key), so its links must NOT be the apikey-sealed feed /dl URL (which would carry
// an empty apikey and 401 at grab). They are sealed to /api/indexers/{slug}/download/
// {token} instead — the same authenticated group, so a cookie-authenticated browser (or
// an X-API-Key caller of this JSON API) can fetch them. The feed's apikey /dl stays for
// *arr.
func (rt *router) resolveSearchLinks(r *http.Request, idx torznabhttp.Indexer, releases []*normalizer.Release) []*normalizer.Release {
	rw := torznabhttp.NewManagementDLRewriter(rt.dlToken, idx, torznabhttp.DownloadBaseURL(r, rt.basePath, idx.Info().ID))
	withhold := rw == nil && torznabhttp.NeedsDLProxy(idx)
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

// downloadRelease streams a search result's .torrent/.nzb bytes (or 302s a magnet) for
// a session- or key-authenticated caller — the session-cookie sibling of the feed's
// apikey /dl proxy, for the web UI (which never sends X-API-Key, so the /dl apikey gate
// 401s it). It reuses the Torznab proxy's resolve/stream core (torznabhttp.ServeGrab),
// decoding the opaque token the JSON search response sealed into the release link, so
// the passkey stays server-side. An unknown or disabled slug is a 404; an invalid token
// is a 400; a resolve failure is a redacted 500.
func (rt *router) downloadRelease(w http.ResponseWriter, r *http.Request) {
	idx, ok := rt.registry.Indexer(r.Context(), chi.URLParam(r, "slug"))
	if !ok {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	torznabhttp.ServeGrab(w, r, idx, rt.dlToken, rt.log, chi.URLParam(r, "token"))
}
