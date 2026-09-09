// Package httpx holds the transport primitives the login and search stages
// previously kept in sync by hand. IsRedirectStatus and Doer were defined
// byte-identically in both stages and are now shared here. ResolveLocation is
// now BOTH stages' Location resolution: search used to keep its own copy that
// returned an absolute Location verbatim, which turned out to diverge from
// Jackett (autobrr/harbrr#329) — see the note on ResolveLocation itself. Resolve
// is the same `new Uri(base, ref)` under it, and is now every stage's
// base-relative URL resolution (login paths and form actions, search and
// download paths, normalizer links), which had drifted into five copies.
// The request-issuing LOOPS (send, do, doSearchRequest, newRequest,
// followRedirects) stay per-stage — header templating, UA-replay source, body
// caps, and error taxonomy genuinely diverge between login and search, so
// only the primitives move here.
package httpx

import (
	"fmt"
	"maps"
	stdhttp "net/http"
	"net/url"
	"strings"

	apphttp "github.com/autobrr/harbrr/internal/http"
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
// embed a secret.
//
// An ABSOLUTE Location is resolved too, not returned verbatim, which is what
// removes its "." / ".." segments. That is the parity-relevant half
// (autobrr/harbrr#329): Jackett resolves every Location against the request URI
// unconditionally — one `new Uri(requestUri, location)` with no absolute/relative
// branch — and .NET canonicalizes the path while constructing that Uri, so the
// string Jackett follows has its dot segments already removed. Search kept a
// second copy that skipped resolution for an absolute Location and so followed
// the raw path; both stages now share this one.
func ResolveLocation(resp *stdhttp.Response, reqURL string) string {
	if !IsRedirectStatus(resp.StatusCode) {
		return ""
	}
	loc := resp.Header.Get("Location")
	if loc == "" {
		return ""
	}
	resolved, err := Resolve(reqURL, loc)
	if err != nil {
		return ""
	}
	return resolved
}

// Resolve resolves a possibly-relative reference against an absolute base,
// reproducing Jackett's resolvePath — one `new Uri(base, ref)` with no
// absolute/relative branch. There is deliberately no absolute-ref
// short-circuit: ResolveReference returns an absolute ref unchanged apart from
// dot-segment removal, which is exactly what .NET's Uri constructor does while
// building the Uri (see the ResolveLocation note above — that is this function
// applied to a 3xx Location header).
//
// A non-absolute base is an error rather than a silent garbage resolve, so a
// caller with no base to resolve against (the normalizer's optional links) can
// fall back to the raw reference.
//
// Errors are pre-redacted to scheme://host: both base and ref routinely carry a
// passkey, and a url.Parse failure quotes its raw input.
func Resolve(base, ref string) (string, error) {
	b, err := url.Parse(base)
	if err != nil {
		return "", fmt.Errorf("parsing base URL %s: %w", apphttp.SchemeHost(base), apphttp.RedactURLError(err))
	}
	if !b.IsAbs() {
		return "", fmt.Errorf("base URL %s is not absolute", apphttp.SchemeHost(base))
	}
	r, err := url.Parse(ref)
	if err != nil {
		return "", fmt.Errorf("parsing %s: %w", apphttp.SchemeHost(ref), apphttp.RedactURLError(err))
	}
	return b.ResolveReference(r).String(), nil
}

// WithFormContentType returns a copy of in with a form-urlencoded Content-Type
// added when the caller did not already set one (matched case-insensitively,
// since definition-authored header names are free-form). The input map is never
// mutated; login and search both post form bodies and need the same behavior.
func WithFormContentType(in map[string][]string) map[string][]string {
	out := make(map[string][]string, len(in)+1)
	maps.Copy(out, in)
	for k := range out {
		if strings.EqualFold(k, "Content-Type") {
			return out
		}
	}
	out["Content-Type"] = []string{"application/x-www-form-urlencoded"}
	return out
}
