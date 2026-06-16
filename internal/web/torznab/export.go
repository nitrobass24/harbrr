package torznab

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

// SearchReleases runs the shared read pipeline behind the Torznab feed's general
// search (t=search) and returns the processed releases: it maps the request params
// to the engine query, searches, de-duplicates by guid, drops categories the query
// did not ask for, and paginates — identical to what the feed serializes for the
// same params. The management API's JSON search calls this so its result set is the
// same as the feed's (the parity guarantee); the only differences are the wire
// format (JSON vs XML) and that the caller resolves resolver-needing links itself
// via NewDLRewriter. It does NOT validate the t= mode (the JSON endpoint is general
// search); a caller needing mode gating does it before calling.
func SearchReleases(ctx context.Context, idx Indexer, q url.Values) ([]*normalizer.Release, error) {
	return searchReleases(ctx, idx, idx.Capabilities(), q)
}

// searchReleases is the shared pipeline worker. writeResults passes the caps it
// already resolved (for mode validation) so they are not recomputed.
func searchReleases(ctx context.Context, idx Indexer, caps *mapper.Capabilities, q url.Values) ([]*normalizer.Release, error) {
	query, requestedCats := buildQuery(q, caps)
	releases, err := idx.Search(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("torznab: search: %w", err)
	}
	// Jackett pipeline order: FixResults (dedupe) -> FilterResults (category drop) -> page.
	releases = filterResults(dedupeByGUID(releases), requestedCats, caps)
	return parsePaging(q).apply(releases), nil
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
func NewDLRewriter(kr *secrets.Keyring, idx Indexer, dlBase, apiKey string) tzn.AcquisitionRewriter {
	if kr == nil || !idx.NeedsResolver() {
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
