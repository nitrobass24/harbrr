package api

import (
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/go-chi/chi/v5"

	apphttp "github.com/autobrr/harbrr/internal/http"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/mapper"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/normalizer"
	"github.com/autobrr/harbrr/internal/indexer/core"
	"github.com/autobrr/harbrr/internal/indexer/grab"
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
func newSearchResponse(res core.SearchResult, results []*normalizer.Release) searchResponse {
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
// read pipeline (core.SearchReleases), so the result set is identical to the
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
	res, err := core.SearchReleases(r.Context(), idx, r.URL.Query())
	// Same contract the feed follows: a degenerate-query skip (autobrr/harbrr#394) is
	// this indexer declining to be asked, not a failure, so it answers with the empty
	// page the pipeline hands back rather than a 500.
	if errors.Is(err, core.ErrDegenerateQuery) {
		err = nil
	}
	if err != nil {
		rt.writeServiceError(w, "search indexer", err)
		return
	}
	res = rt.dropAdultReleases(res, r.URL.Query())
	writeJSON(w, http.StatusOK, newSearchResponse(res, rt.resolveSearchLinks(r, idx, res.Releases)))
}

// dropAdultReleases removes XXX-category results from a UI search when the
// operator hid adult categories AND the request named no categories of its own
// (autobrr/harbrr#383: "searches issued with no explicit category do not return
// adult-category results"). A request that carries `cat` is the operator asking
// for exactly those categories, so it is honored unchanged — including an
// explicit XXX category.
//
// Total is reduced by what this page dropped: it stays an honest floor for the
// remaining match count rather than promising results the filter removed. The
// releases slice is rebuilt, never mutated in place, so the engine's (possibly
// cached) result set is untouched.
//
// This runs on the management JSON search only. The Torznab/Newznab feed serves
// the same query unfiltered — feed consumers are automation carrying their own
// category selection, not an operator looking at a screen.
func (rt *router) dropAdultReleases(res core.SearchResult, q url.Values) core.SearchResult {
	if !rt.hidesAdult(q) {
		return res
	}
	kept := make([]*normalizer.Release, 0, len(res.Releases))
	for _, rel := range res.Releases {
		if !isAdultRelease(rel) {
			kept = append(kept, rel)
		}
	}
	res.Total -= len(res.Releases) - len(kept)
	res.Releases = kept
	return res
}

// hidesAdult reports whether this management search must drop adult-category
// results: the operator hid them and the request named no categories of its own.
func (rt *router) hidesAdult(q url.Values) bool {
	return rt.adultCats.Hidden() && strings.TrimSpace(q.Get("cat")) == ""
}

// isAdultRelease reports whether rel declares an adult category. A nil release is
// not adult — it is dropped by the serializer, not by this filter.
func isAdultRelease(rel *normalizer.Release) bool {
	return rel != nil && slices.ContainsFunc(rel.Categories, mapper.IsAdultCategory)
}

// aggregateSearchResponse is the JSON body of GET /api/search — the management-API
// sibling of the `all`/`profile:` Torznab feeds, and the only search shape the web UI
// runs on. Results are ONE merged window in server order (publish-date desc), Members is
// the per-member ledger behind it, and Total is the merged set this request actually
// fetched: a floor the server stands behind, never a sum of member claims and never a
// promise that a deeper page exists (autobrr/harbrr#372 / #396).
type aggregateSearchResponse struct {
	Results []aggregateSearchResult `json:"results"`
	Members []searchMemberStatus    `json:"members"`
	Total   int                     `json:"total"`
	Limit   int                     `json:"limit"`
	Offset  int                     `json:"offset"`
}

// aggregateSearchResult is a release plus the slug of the member it came from. The
// origin travels with the release because it is what the link is sealed against and
// what the UI's indexer column names — an aggregate response has no single indexer.
type aggregateSearchResult struct {
	Indexer string              `json:"indexer"`
	Release *normalizer.Release `json:"release"`
}

// searchMemberStatus is one ledger row: what harbrr asked, and what it got back. Reason
// is one of core's closed Skip* constants — a member's raw error routinely embeds a
// passkey-bearing URL and never crosses the wire (it goes to the log, redacted).
type searchMemberStatus struct {
	Slug   string `json:"slug"`
	Name   string `json:"name"`
	Status string `json:"status"` // "ok" | "skipped"
	Reason string `json:"reason,omitempty"`
	Count  int    `json:"count"`
}

// searchAggregate runs one search across an EXPLICIT indexer subset and serves the
// server-merged window plus the ledger — the same core.SearchAggregate the aggregate
// feed fans out with, so the page and the feed can never disagree about sort or counts.
// `indexers` is a required comma-separated slug list (the UI's picker); a slug that
// names no configured indexer is a 400 (a client bug), while a configured member that
// is disabled, unbuildable or skipped by its own gates becomes a ledger row and the
// request still succeeds. No member failure can fail the request.
func (rt *router) searchAggregate(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	slugs := splitSlugs(q.Get("indexers"))
	if len(slugs) == 0 {
		writeError(w, http.StatusBadRequest, "indexers is required")
		return
	}
	members, err := rt.registry.Members(r.Context(), slugs)
	if errors.Is(err, core.ErrNoSuchFeed) {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err != nil {
		rt.writeServiceError(w, "search indexers", err)
		return
	}
	res := rt.dropAdultAggregateReleases(core.SearchAggregate(r.Context(), members, q), q)
	rt.logMemberFailures(res.Members)
	writeJSON(w, http.StatusOK, aggregateSearchResponse{
		Results: rt.sealByOrigin(r, res.Releases),
		Members: memberLedger(res.Members),
		Total:   res.Total,
		Limit:   res.Limit,
		Offset:  res.Offset,
	})
}

// splitSlugs parses the comma-separated `indexers` list, dropping blanks so a trailing
// comma or an empty value is not a phantom slug.
func splitSlugs(list string) []string {
	out := make([]string, 0, strings.Count(list, ",")+1)
	for s := range strings.SplitSeq(list, ",") {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// sealByOrigin seals every release's download link against the member it actually came
// from and returns the window in SERVER ORDER. Grouping by origin is the point: a
// release's link is passkey-bearing, and sealing it against the wrong indexer would mint
// a token that grabs from a tracker that never served it. resolveSearchLinks is called
// once per origin (it builds one rewriter per call), and each sealed copy is written
// back to the index it came from.
func (rt *router) sealByOrigin(r *http.Request, releases []core.AggregateRelease) []aggregateSearchResult {
	byOrigin := map[string][]int{}
	for i, rel := range releases {
		id := rel.Indexer.Info().ID
		byOrigin[id] = append(byOrigin[id], i)
	}
	out := make([]aggregateSearchResult, len(releases))
	for id, idxs := range byOrigin {
		batch := make([]*normalizer.Release, len(idxs))
		for j, i := range idxs {
			batch[j] = releases[i].Release
		}
		sealed := rt.resolveSearchLinks(r, releases[idxs[0]].Indexer, batch)
		for j, i := range idxs {
			out[i] = aggregateSearchResult{Indexer: id, Release: sealed[j]}
		}
	}
	return out
}

// memberLedger renders the served status ledger: one row per member the request
// selected, in resolution order. Only the closed reason vocabulary crosses over.
func memberLedger(members []core.MemberOutcome) []searchMemberStatus {
	out := make([]searchMemberStatus, 0, len(members))
	for _, m := range members {
		status := "skipped"
		if m.OK() {
			status = "ok"
		}
		out = append(out, searchMemberStatus{Slug: m.ID, Name: m.Name, Status: status, Reason: m.Reason, Count: m.Count})
	}
	return out
}

// logMemberFailures records each member failure with the error redacted — the only
// place a member's raw failure is reported, and it goes to the log, never the response.
func (rt *router) logMemberFailures(members []core.MemberOutcome) {
	for _, m := range members {
		if m.Err == nil {
			continue
		}
		rt.log.Warn().
			Str("stage", "search").
			Str("indexer", m.ID).
			Str("reason", m.Reason).
			Str("error", apphttp.RedactError(m.Err)).
			Msg("api: aggregate search: member contributed nothing")
	}
}

// dropAdultAggregateReleases is dropAdultReleases for the merged window: the
// hide-adult-categories setting is a property of the management search surface, so the
// aggregate endpoint honors it exactly as the per-indexer one does (autobrr/harbrr#383).
// Total is reduced by what the window dropped, keeping it an honest floor.
func (rt *router) dropAdultAggregateReleases(res core.AggregateResult, q url.Values) core.AggregateResult {
	if !rt.hidesAdult(q) {
		return res
	}
	kept := make([]core.AggregateRelease, 0, len(res.Releases))
	dropped := make(map[string]int, len(res.Members))
	for _, rel := range res.Releases {
		if isAdultRelease(rel.Release) {
			// Track the origin so the LEDGER stays consistent with what is served: a
			// member row claiming 34 beside 30 rendered rows is the page disagreeing
			// with itself, which is the whole failure this surface exists to avoid.
			dropped[rel.Indexer.Info().ID]++
			continue
		}
		kept = append(kept, rel)
	}
	res.Total -= len(res.Releases) - len(kept)
	res.Releases = kept
	for i := range res.Members {
		res.Members[i].Count -= dropped[res.Members[i].ID]
	}
	return res
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
func (rt *router) resolveSearchLinks(r *http.Request, idx core.Indexer, releases []*normalizer.Release) []*normalizer.Release {
	rw := grab.NewManagementDLRewriter(rt.dlToken, idx, grab.DownloadBaseURL(r, rt.urlCfg, idx.Info().ID))
	withhold := rw == nil && grab.NeedsDLProxy(idx)
	out := make([]*normalizer.Release, len(releases))
	for i, rel := range releases {
		cp := *rel
		switch {
		case rw != nil:
			acq := cp.Link
			if acq == "" {
				acq = cp.Magnet
			}
			if link, _, ok := rw(acq, cp.Title, cp.Categories); ok {
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
// 401s it). It reuses the Torznab proxy's resolve/stream core (grab.ServeGrab),
// decoding the opaque token the JSON search response sealed into the release link, so
// the passkey stays server-side. An unknown or disabled slug is a 404; an invalid token
// is a 400; a resolve failure is a redacted 500.
func (rt *router) downloadRelease(w http.ResponseWriter, r *http.Request) {
	idx, ok := rt.registry.Indexer(r.Context(), chi.URLParam(r, "slug"))
	if !ok {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	// writeError keeps every failure in this route's JSON error envelope; the feed's
	// /dl sibling renders the same failures as Torznab XML.
	grab.ServeGrab(w, r, idx, rt.dlToken, rt.log, chi.URLParam(r, "token"), writeError)
}
