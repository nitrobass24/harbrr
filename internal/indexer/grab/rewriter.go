package grab

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/mapper"
	"github.com/autobrr/harbrr/internal/indexer/core"
	"github.com/autobrr/harbrr/internal/secrets"
	tzn "github.com/autobrr/harbrr/internal/torznab"
)

// NeedsDLProxy reports whether an indexer's served links must be routed through the
// /dl proxy rather than served bare: either the def resolves the link before a grab
// (NeedsResolver) or the download authenticates out-of-band by session/header
// (DownloadNeedsAuth). The two routing call sites (the Torznab handler and the JSON
// search API) share this so they seal links identically.
func NeedsDLProxy(idx core.Indexer) bool {
	return idx.NeedsResolver() || idx.DownloadNeedsAuth()
}

// NewDLRewriter builds the acquisition rewriter that seals a resolver-needing
// indexer's passkey-bearing link behind an opaque /dl proxy URL (the same one the
// Torznab feed uses), so the secret never reaches a consumer. It returns nil when
// the proxy is disabled (kr == nil) or the indexer needs no resolution — callers
// then serve the raw link as-is. dlBase is the absolute /dl base (see DLBaseURL);
// apiKey is the caller's own key, echoed into the URL so a later grab authenticates.
// A magnet (public) is kept as-is; a token-mint failure emits a /dl URL with an
// empty token (rejected at grab time) rather than leaking the passkey. The rewriter
// sanitizes and seals the supplied release title as filename metadata.
func NewDLRewriter(kr *secrets.Keyring, idx core.Indexer, dlBase, apiKey string) tzn.AcquisitionRewriter {
	return sealingRewriter(kr, idx, func(token string) string {
		return dlURLWithToken(dlBase, apiKey, token)
	})
}

// NewManagementDLRewriter is NewDLRewriter's sibling for the JSON search API the web UI
// consumes: it seals a resolver-needing indexer's passkey-bearing link into an opaque
// token appended as a path segment to the session-authed management download route
// (downloadBase + "/" + token), instead of the apikey-query feed /dl URL. The token is
// base64url (RawURLEncoding), so it is path-safe. A cookie-authenticated browser can
// fetch the result without presenting an API key. Returns nil when the proxy is disabled
// or the indexer needs no resolution (callers serve the raw link); a magnet is kept
// as-is; a token-mint failure emits a tokenless URL (rejected at grab) rather than
// leaking the passkey. The supplied release title becomes sanitized filename metadata.
func NewManagementDLRewriter(kr *secrets.Keyring, idx core.Indexer, downloadBase string) tzn.AcquisitionRewriter {
	return sealingRewriter(kr, idx, func(token string) string {
		return downloadBase + "/" + token
	})
}

// sealingRewriter is the shared body of the two rewriter constructors — everything
// except how a token becomes a URL. A magnet (public) is kept as-is; a token-mint
// failure emits the URL with an empty token (rejected at grab time) rather than
// leaking the passkey.
func sealingRewriter(kr *secrets.Keyring, idx core.Indexer, urlFor func(token string) string) tzn.AcquisitionRewriter {
	if kr == nil || !NeedsDLProxy(idx) {
		return nil
	}
	indexerID := idx.Info().ID
	return func(original, title string, categories []int) (link, guid string, ok bool) {
		if original == "" || strings.HasPrefix(original, "magnet:") {
			return "", "", false
		}
		token, err := encodeDLToken(kr, indexerID, mapper.PrimaryParentID(categories), title, original)
		if err != nil {
			token = ""
		}
		return urlFor(token), stableGUID(indexerID, original), true
	}
}

// stableGUID derives a deterministic, passkey-free guid from the indexer id and the
// original link, so a proxied release keeps a stable identity across polls (the /dl
// token rotates per request and the original link may embed a passkey).
func stableGUID(indexerID, original string) string {
	sum := sha256.Sum256([]byte(indexerID + "\x00" + original))
	return "harbrr-" + hex.EncodeToString(sum[:])
}
