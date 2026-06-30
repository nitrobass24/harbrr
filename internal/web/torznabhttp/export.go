package torznabhttp

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/mapper"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/normalizer"
	"github.com/autobrr/harbrr/internal/secrets"
	tzn "github.com/autobrr/harbrr/internal/torznab"
)

// SearchResult is the processed output of the shared read pipeline: the releases for
// the requested page plus the paging metadata the feed and the JSON API report. Total
// is the full match count after dedupe+filter but BEFORE the page slice, so a consumer
// can see how many results exist beyond the current window; Offset/Limit are the
// resolved page bounds (after clamping). It is what both surfaces page over identically.
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
// via NewDLRewriter. It does NOT validate the t= mode (the JSON endpoint is general
// search); a caller needing mode gating does it before calling.
func SearchReleases(ctx context.Context, idx Indexer, q url.Values) (SearchResult, error) {
	return searchReleases(ctx, idx, idx.Capabilities(), q)
}

// searchReleases is the shared pipeline worker. writeResults passes the caps it
// already resolved (for mode validation) so they are not recomputed.
func searchReleases(ctx context.Context, idx Indexer, caps *mapper.Capabilities, q url.Values) (SearchResult, error) {
	query, requestedCats := buildQuery(q, caps)
	if wantsNoCache(q) {
		ctx = WithCacheBypass(ctx)
	}
	pg := parsePaging(q)
	// Carry the page window into the engine query. A paging-capable driver (Newznab)
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
	if pager, ok := idx.(OffsetPager); ok && pager.SupportsOffsetPaging() {
		return pagedResult(releases, pg, rawCount), nil
	}
	return localPageResult(releases, pg), nil
}

// localPageResult is the non-paging path (every Cardigann def, every non-Newznab native
// driver): the driver returned the FULL result set, so Total is the real match count
// pre-slice and the page is sliced locally to [offset, offset+limit).
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

// DLBaseURL builds the externally-visible /dl endpoint base for an indexer from the
// request scheme/host and the configured base path — the same URL the Torznab feed
// emits. The apikey and token are appended per release by NewDLRewriter.
func DLBaseURL(r *http.Request, basePath, indexerID string) string {
	scheme := "http"
	if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
		scheme = "https"
	}
	return scheme + "://" + r.Host + basePath + "/api/v2.0/indexers/" + url.PathEscape(indexerID) + "/dl"
}

// NewDLRewriter builds the acquisition rewriter that seals a resolver-needing
// indexer's passkey-bearing link behind an opaque /dl proxy URL (the same one the
// Torznab feed uses), so the secret never reaches a consumer. It returns nil when
// the proxy is disabled (kr == nil) or the indexer needs no resolution — callers
// then serve the raw link as-is. dlBase is the absolute /dl base (see DLBaseURL);
// apiKey is the caller's own key, echoed into the URL so a later grab authenticates.
// A magnet (public) is kept as-is; a token-mint failure emits a /dl URL with an
// empty token (rejected at grab time) rather than leaking the passkey.
// NeedsDLProxy reports whether an indexer's served links must be routed through the
// /dl proxy rather than served bare: either the def resolves the link before a grab
// (NeedsResolver) or the download authenticates out-of-band by session/header
// (DownloadNeedsAuth). The two routing call sites (the Torznab handler and the JSON
// search API) share this so they seal links identically.
func NeedsDLProxy(idx Indexer) bool {
	return idx.NeedsResolver() || idx.DownloadNeedsAuth()
}

func NewDLRewriter(kr *secrets.Keyring, idx Indexer, dlBase, apiKey string) tzn.AcquisitionRewriter {
	if kr == nil || !NeedsDLProxy(idx) {
		return nil
	}
	indexerID := idx.Info().ID
	return func(original string) (link, guid string, ok bool) {
		if original == "" || strings.HasPrefix(original, "magnet:") {
			return "", "", false
		}
		token, err := encodeDLToken(kr, indexerID, original)
		if err != nil {
			return dlURLWithToken(dlBase, apiKey, ""), stableGUID(indexerID, original), true
		}
		return dlURLWithToken(dlBase, apiKey, token), stableGUID(indexerID, original), true
	}
}
