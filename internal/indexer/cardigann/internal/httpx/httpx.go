// Package httpx holds the transport primitives the login and search stages
// previously kept in sync by hand. IsRedirectStatus and Doer were defined
// byte-identically in both stages and are now shared here. ResolveLocation is
// LOGIN's Location resolution only: search's redirectTarget deliberately stays
// on its own resolveURL, which returns an absolute Location verbatim instead
// of dot-segment-cleaning it — a real divergence confirmed by issue #324's
// equivalence check (see the redirectTarget comment in search/request.go).
// The request-issuing LOOPS (send, do, doSearchRequest, newRequest,
// followRedirects) stay per-stage — header templating, UA-replay source, body
// caps, and error taxonomy genuinely diverge between login and search, so
// only the primitives move here.
package httpx

import (
	stdhttp "net/http"
	"net/url"
)

// Doer is the narrow HTTP seam every cardigann stage drives: satisfied by
// *http.Client in production and a replay transport in tests, so no live
// network call ever happens in engine code or its tests.
type Doer interface {
	Do(*stdhttp.Request) (*stdhttp.Response, error)
}

// IsRedirectStatus reports whether status is a Location-bearing redirect,
// matching Jackett's WebResult.IsRedirect semantics: 301 (Moved Permanently),
// 302 (Found), 303 (See Other), 307 (Temporary Redirect), 308 (Permanent
// Redirect). Two accepted divergences from Jackett, recorded in
// parity/testdata/README.md: Jackett omits 308 (harbrr treats it like the
// other redirect codes — no corpus def emits one), and Jackett also counts
// ANY response carrying a Refresh header as a redirect (an obsolete
// Cloudflare interstitial pattern; harbrr's anti-bot handling lives at the
// solver boundary instead).
func IsRedirectStatus(status int) bool {
	switch status {
	case stdhttp.StatusMovedPermanently, stdhttp.StatusFound, stdhttp.StatusSeeOther,
		stdhttp.StatusTemporaryRedirect, stdhttp.StatusPermanentRedirect:
		return true
	default:
		return false
	}
}

// ResolveLocation resolves a 3xx response's Location header against reqURL, so
// a relative Location works regardless of whether the Doer set resp.Request.
// Returns "" when the response is not a redirect or carries no usable
// Location. The result is never logged raw — like the request URL, it can
// embed a secret. Adopted by LOGIN only: search's redirectTarget stays on its
// own resolveURL (absolute Locations verbatim; see search/request.go).
func ResolveLocation(resp *stdhttp.Response, reqURL string) string {
	if !IsRedirectStatus(resp.StatusCode) {
		return ""
	}
	loc := resp.Header.Get("Location")
	if loc == "" {
		return ""
	}
	base, err := url.Parse(reqURL)
	if err != nil {
		return ""
	}
	ref, err := url.Parse(loc)
	if err != nil {
		return ""
	}
	return base.ResolveReference(ref).String()
}
