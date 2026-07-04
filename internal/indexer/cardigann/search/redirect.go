package search

import (
	"context"
	"fmt"
	stdhttp "net/http"
	"net/url"

	apphttp "github.com/autobrr/harbrr/internal/http"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/loader"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/login"
)

// maxRedirectHops caps the manual follow at Jackett's FollowIfRedirect default
// (maxRedirects = 5).
const maxRedirectHops = 5

// isRedirectStatus reports whether status is a Location-bearing redirect:
// 301, 302, 303, 307, 308. Two accepted divergences from Jackett's
// WebResult.IsRedirect, recorded in parity/testdata/README.md: Jackett omits
// 308 (harbrr treats it like 301 — no corpus def emits one), and Jackett also
// counts ANY response carrying a Refresh header as a redirect (an obsolete
// Cloudflare interstitial pattern; harbrr's anti-bot handling lives at the
// solver boundary instead).
func isRedirectStatus(status int) bool {
	switch status {
	case stdhttp.StatusMovedPermanently, stdhttp.StatusFound, stdhttp.StatusSeeOther,
		stdhttp.StatusTemporaryRedirect, stdhttp.StatusPermanentRedirect:
		return true
	default:
		return false
	}
}

// resolveRedirect maps a 3xx search response to its final outcome, reproducing
// Jackett's PerformQuery redirect handling:
//
//   - the path opted in via `followredirect` → follow manually (followRedirects);
//     a chain that ends non-3xx is the response to parse.
//   - an XML path parses the redirect body as-is: Jackett's XML branch never
//     runs CheckIfLoginIsNeeded — no relogin, no re-request, login block or not.
//   - otherwise Jackett's CheckIfLoginIsNeeded fires on ANY redirect: a def with
//     a login block gets ErrSearchLoggedOut so the engine re-logins and retries
//     once; a def without one still gets ONE re-request (Jackett's DoLogin
//     no-ops, then re-requests unconditionally and parses the second response —
//     not wasted: the 302's Set-Cookie is already in the client jar, so a
//     cookie-gate tracker succeeds on the retry).
//
// Deliberate divergence: Jackett additionally throws "Got redirected to another
// domain" for a cross-domain redirect; harbrr does not inspect the target
// domain — the logged-out error / re-request covers both cases.
func resolveRedirect(ctx context.Context, doer Doer, br builtRequest, first searchResponse, def *loader.Definition, session *login.Session) (searchResponse, error) {
	sr := first
	if br.followRedirect {
		followed, err := followRedirects(ctx, doer, sr, session)
		if err != nil {
			return searchResponse{}, err
		}
		if !isRedirectStatus(followed.status) {
			return followed, nil
		}
		// Hop cap exhausted or magnet target: fall through to the unfollowed
		// mapping below. This is Jackett-exact — FollowIfRedirect's magnet break
		// leaves the response still IsRedirect, and the search flow's
		// CheckIfLoginIsNeeded then fires on it (relogin / re-request); a magnet
		// Location is terminal only in Jackett's DOWNLOAD flow, not in search.
		sr = followed
	}
	if br.respType == responseTypeXML {
		return sr, nil
	}
	if def.Login != nil {
		return searchResponse{}, ErrSearchLoggedOut
	}
	return doSearchRequest(ctx, doer, br, session)
}

// followRedirects reproduces Jackett's FollowIfRedirect for a search response:
// up to maxRedirectHops hops, each re-issued as a bare GET (no method, body, or
// definition headers carried over). A magnet Location stops the loop with the
// redirect response intact (Jackett's explicit magnet break); any other
// non-http(s) scheme is a loud error (Jackett's HttpClient would throw). A 3xx
// without a Location also stops with the response as-is. Hops go back through
// doSearchRequest, so each one is individually paced/retried by the production
// client and can never be auto-followed.
//
// Accepted divergence (recorded in parity/testdata/README.md): hops carry the
// session cookies + solver UA (applySession, plus the production client's own
// jar). Jackett's SEARCH-path FollowIfRedirect issues its hops with NO cookies
// (defaults: overrideCookies=null, accumulateCookies=false → an anonymous
// WebRequest), which lands a logged-in def's redirected search on the login
// page. harbrr deliberately keeps the hop authenticated — the additive behavior
// every followredirect+login def (kinozal, selezen, bjshare, hhanclub) actually
// wants — and the production jar could not be bypassed per-request anyway.
func followRedirects(ctx context.Context, doer Doer, sr searchResponse, session *login.Session) (searchResponse, error) {
	for hop := 0; hop < maxRedirectHops && isRedirectStatus(sr.status); hop++ {
		if sr.location == "" {
			return sr, nil
		}
		target, err := url.Parse(sr.location)
		if err != nil {
			return searchResponse{}, fmt.Errorf("parsing redirect target %s: %w", apphttp.SchemeHost(sr.location), apphttp.RedactURLError(err))
		}
		switch target.Scheme {
		case "http", "https":
		case "magnet":
			return sr, nil
		default:
			return searchResponse{}, fmt.Errorf("search: redirect to unsupported scheme %q", target.Scheme)
		}
		next, err := doSearchRequest(ctx, doer, builtRequest{method: stdhttp.MethodGet, url: sr.location}, session)
		if err != nil {
			return searchResponse{}, err
		}
		sr = next
	}
	return sr, nil
}
