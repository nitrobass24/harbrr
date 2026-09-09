package http

import (
	"context"
	"errors"
	"fmt"
	stdhttp "net/http"
	"strings"
)

// noRedirectFollowKey marks a request whose redirects the caller handles itself.
type noRedirectFollowKey struct{}

// WithNoRedirectFollow marks ctx so a client using RedirectPolicy surfaces a 3xx
// response to the caller instead of following it. The Cardigann search stage
// stamps every search-path request with this: Jackett's WebClient never
// auto-follows, and the engine needs the raw 3xx both to honor a path's
// `followredirect` opt-in manually (Jackett FollowIfRedirect) and to treat an
// unexpected redirect as a logged-out signal (CheckIfLoginIsNeeded).
func WithNoRedirectFollow(ctx context.Context) context.Context {
	return context.WithValue(ctx, noRedirectFollowKey{}, true)
}

// RedirectPolicy is the http.Client CheckRedirect for clients shared between
// redirect-following flows (login, download/grab, native drivers) and the
// no-follow search stage. Requests stamped with WithNoRedirectFollow get the
// last response back unfollowed; everything else keeps the stdlib default
// behavior — including the 10-hop cap, which a custom CheckRedirect must
// re-implement because installing one replaces defaultCheckRedirect entirely.
func RedirectPolicy(req *stdhttp.Request, via []*stdhttp.Request) error {
	if req.Context().Value(noRedirectFollowKey{}) != nil {
		return stdhttp.ErrUseLastResponse
	}
	if len(via) >= 10 {
		return errors.New("stopped after 10 redirects")
	}
	return nil
}

// RefuseCrossHostRedirect is the http.Client CheckRedirect for the shared app-facing
// client (apps, appsync, notify, download, announce). Those services authenticate to
// Servarr/qui/cross-seed with custom X-API-Key / X-Api-Key headers, and Go strips only
// Authorization, Cookie and WWW-Authenticate on a cross-domain hop — a custom header
// follows an open redirect to whatever host answered. So a hop to a different hostname
// (port ignored) is refused when the ORIGINAL request carried any header at all.
//
// A header-less request keeps following (the stdlib default): two bare GETs ride this
// client — blackhole's passthrough fetch of a caller-supplied indexer link
// (internal/download/blackhole.go fetchBytes) and announce's fetch of harbrr's own /dl
// (internal/announce/factory.go HTTPTorrentFetcher) — and usenet indexers routinely 302
// nzb links to other hosts. Go builds the redirected request from Location, so the
// original URL (and any apikey in its query) travels only as the Referer Go adds before
// calling CheckRedirect; that header is dropped on a cross-host hop so the redirect
// target learns nothing about the URL it was reached from.
//
// Installing a CheckRedirect replaces defaultCheckRedirect entirely, so the 10-hop cap
// is re-implemented here. The error names both hosts (hosts are not secrets) and
// nothing else — no path, no query string.
func RefuseCrossHostRedirect(req *stdhttp.Request, via []*stdhttp.Request) error {
	if len(via) >= 10 {
		return errors.New("stopped after 10 redirects")
	}
	from, to := via[0].URL.Hostname(), req.URL.Hostname()
	if strings.EqualFold(from, to) { // hostnames are case-insensitive
		return nil
	}
	if len(via[0].Header) == 0 {
		req.Header.Del("Referer")
		return nil
	}
	return fmt.Errorf("refusing redirect from host %q to %q (would leak request headers)", from, to)
}
