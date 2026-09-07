package grab

import (
	"net/http"
	"net/url"

	apphttp "github.com/autobrr/harbrr/internal/http"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/mapper"
	"github.com/autobrr/harbrr/internal/secrets"
)

// URLConfig is the shared input the absolute-URL builders (DLBaseURL, DownloadBaseURL,
// the feed package's FeedURL, and the feed handler's own self URL) need beyond the
// *http.Request: BasePath is re-added after the server strips it; ExternalOrigin
// ("scheme://host"), when set, is authoritative over the request-derived origin;
// TrustedProxies gates X-Forwarded-Proto trust in that request-derived fallback.
type URLConfig struct {
	BasePath       string
	ExternalOrigin string
	TrustedProxies apphttp.TrustedProxies
}

// DLBaseURL builds the externally-visible /dl endpoint base for an indexer — the same
// URL the Torznab feed emits. The apikey and token are appended per release by
// NewDLRewriter.
func DLBaseURL(r *http.Request, cfg URLConfig, indexerID string) string {
	return ExternalIndexerBase(r, cfg, indexerID) + "/dl"
}

// DownloadBaseURL builds the externally-visible session-authed management download
// endpoint base for an indexer (…/api/indexers/{slug}/download); NewManagementDLRewriter
// appends /{token} per release. Unlike the feed /dl URL it carries NO apikey — the
// management route authenticates by session cookie or X-API-Key, so the web UI (which
// authenticates by cookie and never sends X-API-Key) can fetch a release the apikey-
// sealed /dl would 401.
func DownloadBaseURL(r *http.Request, cfg URLConfig, indexerID string) string {
	return ExternalIndexerBase(r, cfg, indexerID) + "/download"
}

// DLBaseURLForOrigin builds the same /dl endpoint base as DLBaseURL but from an explicit
// origin (scheme://host), for callers that have no *http.Request — the announce
// background service derives the origin from the stored connection URL. It shares the
// /api/indexers/<slug>/dl construction with DLBaseURL so the two never drift.
func DLBaseURLForOrigin(origin, basePath, slug string) string {
	return indexerBaseURL(origin, basePath, slug) + "/dl"
}

// SealedDLURL builds an absolute, fetchable /dl proxy URL for an original (passkey-bearing)
// download link: it seals the link into an opaque token bound to indexerID under kr, then
// appends the apikey. The URL resolves and fetches the torrent server-side, so the passkey
// never leaves harbrr. title is the release title (empty when the caller genuinely has
// none), sealed so the eventual grab is named after the release. dlBase is the absolute
// /dl endpoint (origin + base path + /api/indexers/<id>/dl). Used by the cross-seed
// announce source to hand a cross-seed tool a link it can fetch without seeing the
// passkey. The error never carries the link.
func SealedDLURL(kr *secrets.Keyring, indexerID, dlBase, apiKey, title, originalLink string) (string, error) {
	// The announce source carries no category metadata, so the grab is tallied as
	// uncategorised.
	token, err := encodeDLToken(kr, indexerID, mapper.UncategorizedID, title, originalLink)
	if err != nil {
		return "", err
	}
	return dlURLWithToken(dlBase, apiKey, token), nil
}

// ExternalIndexerBase is the shared origin<basePath>/api/indexers/<id> prefix the feed
// and /dl URLs hang off. The origin is cfg.ExternalOrigin when the operator configured
// one; otherwise it derives from the request scheme/host, honoring X-Forwarded-Proto
// only from a trusted proxy peer (apphttp.RequestScheme).
func ExternalIndexerBase(r *http.Request, cfg URLConfig, indexerID string) string {
	origin := cfg.ExternalOrigin
	if origin == "" {
		origin = apphttp.RequestScheme(r, cfg.TrustedProxies) + "://" + r.Host
	}
	return indexerBaseURL(origin, cfg.BasePath, indexerID)
}

// indexerBaseURL is the single builder for the {origin}{basePath}/api/indexers/{slug}
// prefix, so every feed/dl URL (request-derived or origin-explicit) shares one source of
// truth for the path shape.
func indexerBaseURL(origin, basePath, slug string) string {
	return origin + basePath + "/api/indexers/" + url.PathEscape(slug)
}

// dlURLWithToken appends the caller's apikey (so *arr can authenticate the grab) and
// the opaque token to the /dl base URL.
func dlURLWithToken(base, apiKey, token string) string {
	q := url.Values{}
	if apiKey != "" {
		q.Set("apikey", apiKey)
	}
	q.Set("token", token)
	return base + "?" + q.Encode()
}
