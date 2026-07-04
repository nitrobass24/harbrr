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

// isRedirectStatus reports whether status is one Jackett's WebResult.IsRedirect
// recognizes: 301, 302, 303, 307, 308.
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
//   - still (or never) a redirect → Jackett's CheckIfLoginIsNeeded fires on ANY
//     redirect, unconditionally: a def with a login block gets ErrSearchLoggedOut
//     so the engine re-logins and retries once; a def without one has nothing to
//     refresh, so the redirect body is parsed as-is (0 rows → 0 releases,
//     Jackett's terminal outcome — harbrr skips its wasted no-op-login
//     re-request).
//
// Deliberate divergence: Jackett additionally throws "Got redirected to another
// domain" for a cross-domain redirect; harbrr does not inspect the target
// domain — the logged-out error / empty parse covers both cases.
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
		sr = followed // hop cap exhausted or magnet target: fall through unfollowed.
	}
	if def.Login != nil {
		return searchResponse{}, ErrSearchLoggedOut
	}
	return sr, nil
}

// followRedirects reproduces Jackett's FollowIfRedirect for a search response:
// up to maxRedirectHops hops, each re-issued as a bare GET (no method, body, or
// definition headers carried over — only the session cookies + solver UA via
// applySession, matching Jackett's redirect WebRequest carrying only cookies).
// A magnet Location stops the loop with the redirect response intact (Jackett's
// explicit magnet break); any other non-http(s) scheme is a loud error (Jackett's
// HttpClient would throw). A 3xx without a Location also stops with the response
// as-is. Hops go back through doSearchRequest, so each one is individually
// paced/retried by the production client and can never be auto-followed.
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
