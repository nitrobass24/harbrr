package torznabhttp

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/autobrr/harbrr/internal/indexer/core"
	"github.com/autobrr/harbrr/internal/secrets"
	tzn "github.com/autobrr/harbrr/internal/torznab"
)

// DLBaseURL builds the externally-visible /dl endpoint base for an indexer from the
// request scheme/host and the configured base path — the same URL the Torznab feed
// emits. The apikey and token are appended per release by NewDLRewriter.
func DLBaseURL(r *http.Request, basePath, indexerID string) string {
	return externalIndexerBase(r, basePath, indexerID) + "/dl"
}

// DownloadBaseURL builds the externally-visible session-authed management download
// endpoint base for an indexer (…/api/indexers/{slug}/download); NewManagementDLRewriter
// appends /{token} per release. Unlike the feed /dl URL it carries NO apikey — the
// management route authenticates by session cookie or X-API-Key, so the web UI (which
// authenticates by cookie and never sends X-API-Key) can fetch a release the apikey-
// sealed /dl would 401.
func DownloadBaseURL(r *http.Request, basePath, indexerID string) string {
	return externalIndexerBase(r, basePath, indexerID) + "/download"
}

// DLBaseURLForOrigin builds the same /dl endpoint base as DLBaseURL but from an explicit
// origin (scheme://host), for callers that have no *http.Request — the announce
// background service derives the origin from the stored connection URL. It shares the
// /api/indexers/<slug>/dl construction with DLBaseURL so the two never drift.
func DLBaseURLForOrigin(origin, basePath, slug string) string {
	return indexerBaseURL(origin, basePath, slug) + "/dl"
}

// FeedURL builds the externally-visible Torznab results-feed URL for an indexer (no
// apikey appended). bypass selects the freeleech-bypass /full variant — the URL harbrr
// hands cross-seed consumers that must see the full catalog. It reuses the same
// scheme/host/base-path derivation as DLBaseURL so the two stay consistent.
func FeedURL(r *http.Request, basePath, indexerID string, bypass bool) string {
	u := externalIndexerBase(r, basePath, indexerID) + "/results/torznab"
	if bypass {
		u += "/full"
	}
	return u
}

// SealedDLURL builds an absolute, fetchable /dl proxy URL for an original (passkey-bearing)
// download link: it seals the link into an opaque token bound to indexerID under kr, then
// appends the apikey. The URL resolves and fetches the torrent server-side, so the passkey
// never leaves harbrr. dlBase is the absolute /dl endpoint (origin + base path +
// /api/indexers/<id>/dl). Used by the cross-seed announce source to hand a cross-seed
// tool a link it can fetch without seeing the passkey. The error never carries the link.
func SealedDLURL(kr *secrets.Keyring, indexerID, dlBase, apiKey, originalLink string) (string, error) {
	token, err := encodeDLToken(kr, indexerID, originalLink)
	if err != nil {
		return "", err
	}
	return dlURLWithToken(dlBase, apiKey, token), nil
}

// externalIndexerBase is the shared scheme://host<basePath>/api/indexers/<id>
// prefix the feed and /dl URLs hang off, deriving the origin from the request.
func externalIndexerBase(r *http.Request, basePath, indexerID string) string {
	scheme := "http"
	if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
		scheme = "https"
	}
	return indexerBaseURL(scheme+"://"+r.Host, basePath, indexerID)
}

// indexerBaseURL is the single builder for the {origin}{basePath}/api/indexers/{slug}
// prefix, so every feed/dl URL (request-derived or origin-explicit) shares one source of
// truth for the path shape.
func indexerBaseURL(origin, basePath, slug string) string {
	return origin + basePath + "/api/indexers/" + url.PathEscape(slug)
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
func NeedsDLProxy(idx core.Indexer) bool {
	return idx.NeedsResolver() || idx.DownloadNeedsAuth()
}

func NewDLRewriter(kr *secrets.Keyring, idx core.Indexer, dlBase, apiKey string) tzn.AcquisitionRewriter {
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

// NewManagementDLRewriter is NewDLRewriter's sibling for the JSON search API the web UI
// consumes: it seals a resolver-needing indexer's passkey-bearing link into an opaque
// token appended as a path segment to the session-authed management download route
// (downloadBase + "/" + token), instead of the apikey-query feed /dl URL. The token is
// base64url (RawURLEncoding), so it is path-safe. A cookie-authenticated browser can
// fetch the result without presenting an API key. Returns nil when the proxy is disabled
// or the indexer needs no resolution (callers serve the raw link); a magnet is kept
// as-is; a token-mint failure emits a tokenless URL (rejected at grab) rather than
// leaking the passkey.
func NewManagementDLRewriter(kr *secrets.Keyring, idx core.Indexer, downloadBase string) tzn.AcquisitionRewriter {
	if kr == nil || !NeedsDLProxy(idx) {
		return nil
	}
	indexerID := idx.Info().ID
	return func(original string) (link, guid string, ok bool) {
		if original == "" || strings.HasPrefix(original, "magnet:") {
			return "", "", false
		}
		g := stableGUID(indexerID, original)
		token, err := encodeDLToken(kr, indexerID, original)
		if err != nil {
			return downloadBase + "/", g, true
		}
		return downloadBase + "/" + token, g, true
	}
}
