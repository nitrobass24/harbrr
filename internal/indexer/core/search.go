package core

import (
	"context"
	"fmt"
	"net/url"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/mapper"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/normalizer"
	tzn "github.com/autobrr/harbrr/internal/torznab"
)

// SearchResult is the processed output of the shared read pipeline: the releases for
// the requested page plus the paging metadata a serving surface reports. Total
// is the full match count after dedupe+filter but BEFORE the page slice, so a consumer
// can see how many results exist beyond the current window; Offset/Limit are the
// resolved page bounds (after clamping). It is what both the Torznab feed and the
// management JSON search API page over identically.
type SearchResult struct {
	Releases []*normalizer.Release
	Total    int
	Offset   int
	Limit    int
}

// SearchReleases runs the shared read pipeline behind the Torznab feed's general
// search (t=search) and returns the processed releases: it maps the request params
// to the engine query, searches, de-duplicates by guid, drops categories the query
// did not ask for, and paginates — identical to what the feed serializes for the
// same params. The management API's JSON search calls this so its result set is the
// same as the feed's (the parity guarantee); the only differences are the wire
// format (JSON vs XML) and that the caller resolves resolver-needing links itself
// via torznabhttp.NewDLRewriter. It does NOT validate the t= mode (the JSON endpoint
// is general search); a caller needing mode gating does it before calling.
func SearchReleases(ctx context.Context, idx Indexer, q url.Values) (SearchResult, error) {
	return SearchReleasesWithCaps(ctx, idx, idx.Capabilities(), q)
}

// SearchReleasesWithCaps is SearchReleases for a caller that already resolved the
// indexer's capabilities (e.g. for t= mode validation) so they are not recomputed —
// the Torznab handler's writeResults uses this form.
func SearchReleasesWithCaps(ctx context.Context, idx Indexer, caps *mapper.Capabilities, q url.Values) (SearchResult, error) {
	query, requestedCats := buildQuery(q, caps)
	// The bypass feed variant (set on ctx by the Torznab route) asks the registry's
	// freeleech view for the full catalog; carry it onto the engine query the decorator
	// reads.
	query.FreeleechBypass = FreeleechBypass(ctx)
	if WantsNoCache(q) {
		ctx = WithCacheBypass(ctx)
	}
	pg := parsePaging(q)
	// Carry the page window into the engine query. A paging-capable driver (newznab/nzbindex)
	// forwards it upstream for deep-set paging; every other driver ignores it (Offset/
	// Limit are request context, never templated), so the request URL stays byte-identical.
	query.Offset, query.Limit = pg.offset, pg.limit
	releases, err := idx.Search(ctx, query)
	if err != nil {
		return SearchResult{}, fmt.Errorf("torznab: search: %w", err)
	}
	// rawCount is the engine's pre-dedupe page size: a full upstream page (>= limit) means
	// "there is probably more", the +1 has-more floor the paging branch applies below.
	rawCount := len(releases)
	// Jackett pipeline order: FixResults (dedupe) -> FilterResults (category drop) -> page.
	releases = filterResults(dedupeByGUID(releases), requestedCats, caps)
	if idx.SupportsOffsetPaging() {
		return pagedResult(releases, pg, rawCount), nil
	}
	return localPageResult(releases, pg), nil
}

// dedupeByGUID drops releases sharing a guid (Jackett's post-FixResults GroupBy),
// keeping the first occurrence and preserving order, so no serving surface ever sees
// duplicate items. nil entries are skipped defensively. It uses tzn.GUIDFor — the same
// derivation internal/torznab's serializer emits — so a release's identity here matches
// the <guid> a caller renders.
func dedupeByGUID(releases []*normalizer.Release) []*normalizer.Release {
	seen := make(map[string]struct{}, len(releases))
	out := make([]*normalizer.Release, 0, len(releases))
	for _, rel := range releases {
		if rel == nil {
			continue
		}
		guid := tzn.GUIDFor(rel)
		if _, dup := seen[guid]; dup {
			continue
		}
		seen[guid] = struct{}{}
		out = append(out, rel)
	}
	return out
}

// localPageResult is the non-paging path (every Cardigann def, every native driver except
// the newznab/nzbindex usenet pair): the driver returned the FULL result set, so Total
// is the real match count pre-slice and the page is sliced locally to [offset, offset+limit).
func localPageResult(releases []*normalizer.Release, pg paging) SearchResult {
	return SearchResult{
		Releases: pg.apply(releases),
		Total:    len(releases),
		Offset:   pg.offset,
		Limit:    pg.limit,
	}
}

// pagedResult is the paging path (the driver already skipped `offset` upstream, so the
// returned slice IS the requested page — it must NOT be re-offset locally). The slice is
// only clamped to the limit; Total is reported as a running floor: offset + limit + 1 when
// the upstream page came back full (>= limit, so more likely exist), else the exact
// offset + served for a short/last page. This drives *arr's "fetch next page" without the
// driver knowing the grand total (Newznab gives none).
func pagedResult(releases []*normalizer.Release, pg paging, rawCount int) SearchResult {
	served := releases
	if len(served) > pg.limit {
		served = served[:pg.limit]
	}
	total := pg.offset + len(served)
	if rawCount >= pg.limit {
		// Full upstream page: advertise at least one more page. Base the floor on the
		// REQUESTED width, not len(served) — dedupe/category filtering can shrink served
		// below limit, and offset+served+1 could then fall at/under offset+limit, which
		// makes *arr conclude "no next page" and stop before the genuine deep page.
		total = pg.offset + pg.limit + 1
	}
	return SearchResult{
		Releases: served,
		Total:    total,
		Offset:   pg.offset,
		Limit:    pg.limit,
	}
}
