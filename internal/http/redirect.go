package http

import (
	"context"
	"errors"
	stdhttp "net/http"
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
