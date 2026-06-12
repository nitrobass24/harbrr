package http

import (
	"net/http"
	"net/url"
	"strings"
)

// redactedValue is the placeholder substituted for any secret token, header
// value, or query-parameter value. It deliberately carries no information about
// the original (length, prefix, etc.).
const redactedValue = "REDACTED"

// secretQueryParams is the set of query-parameter names whose values are
// treated as secrets and redacted by RedactURL. Cardigann definitions routinely
// embed passkeys, API keys, and download tokens directly in URLs, so this list
// is intentionally broad. Matching is case-insensitive and also catches names
// that merely CONTAIN one of these tokens (e.g. "torrent_pass", "rss_key"),
// because trackers spell these many different ways.
var secretQueryParams = []string{
	"passkey",
	"apikey",
	"api_key",
	"authkey",
	"auth_key",
	"torrent_pass",
	"rsskey",
	"rss_key",
	"secret",
	"token",
	"cookie",
	"passid",
	"pid",
	"auth",
	"key",
}

// secretHeaders is the set of header names whose values are redacted by
// RedactHeader. Canonicalized to http.Header's canonical form for lookup.
var secretHeaders = map[string]struct{}{
	"Authorization":       {},
	"Cookie":              {},
	"Set-Cookie":          {},
	"X-Api-Key":           {},
	"Proxy-Authorization": {},
}

// RedactURL returns raw with the values of any secret query parameters replaced
// by a fixed placeholder, preserving the URL's structure (scheme, host, path,
// the non-secret parameters, fragment). It is safe to call on user/definition
// input: an unparseable URL is redacted conservatively by a textual fallback so
// a malformed passkey-bearing string is never returned verbatim.
//
// This is the single chokepoint every log/error/trace site routes URLs through.
func RedactURL(raw string) string {
	if raw == "" {
		return raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return redactURLFallback(raw)
	}
	redactUserinfo(u)
	if u.RawQuery == "" {
		return u.String()
	}

	q := u.Query()
	for name := range q {
		if isSecretParam(name) {
			vals := q[name]
			for i := range vals {
				vals[i] = redactedValue
			}
		}
	}
	u.RawQuery = q.Encode()
	return u.String()
}

// redactUserinfo scrubs a password embedded in the URL userinfo
// (scheme://user:password@host). Some definitions place credentials there; the
// stdlib would otherwise stringify them verbatim. The username is preserved
// (it is not a secret and aids debugging); only the password is replaced.
func redactUserinfo(u *url.URL) {
	if u.User == nil {
		return
	}
	if _, hasPassword := u.User.Password(); hasPassword {
		u.User = url.UserPassword(u.User.Username(), redactedValue)
	}
}

// redactURLFallback handles input url.Parse rejects. Since the structure can't
// be parsed, a secret may hide anywhere — query, path, or userinfo — so raw is
// never returned verbatim. It keeps only the scheme://host prefix (dropping any
// userinfo, which can embed a password) and redacts everything from the path
// onward. When even a host can't be isolated, the whole string is redacted.
func redactURLFallback(raw string) string {
	scheme, rest := "", raw
	if i := strings.Index(raw, "://"); i >= 0 {
		scheme, rest = raw[:i+len("://")], raw[i+len("://"):]
	}
	// The authority ends at the first path/query/fragment delimiter.
	authority, hasRemainder := rest, false
	if j := strings.IndexAny(rest, "/?#"); j >= 0 {
		authority, hasRemainder = rest[:j], true
	}
	// Drop any userinfo (user:password@): only the host is non-secret.
	if at := strings.LastIndexByte(authority, '@'); at >= 0 {
		authority = authority[at+1:]
	}
	switch {
	case scheme == "" && !hasRemainder:
		// No recognizable URL structure to preserve safely.
		return redactedValue
	case hasRemainder:
		return scheme + authority + "/" + redactedValue
	default:
		return scheme + authority
	}
}

// isSecretParam reports whether a query-parameter name should have its value
// redacted. Matching is case-insensitive and substring-based so the many tracker
// spellings (torrent_pass, rsskey, api_key, ...) are all caught.
func isSecretParam(name string) bool {
	lower := strings.ToLower(name)
	for _, s := range secretQueryParams {
		if strings.Contains(lower, s) {
			return true
		}
	}
	return false
}

// RedactHeader returns a copy of h with the values of sensitive headers
// (Authorization, Cookie, Set-Cookie, ...) replaced by a fixed placeholder. The
// input is never mutated. A nil header returns nil.
func RedactHeader(h http.Header) http.Header {
	if h == nil {
		return nil
	}
	out := make(http.Header, len(h))
	for name, vals := range h {
		if _, secret := secretHeaders[http.CanonicalHeaderKey(name)]; secret {
			out[name] = []string{redactedValue}
			continue
		}
		cp := make([]string, len(vals))
		copy(cp, vals)
		out[name] = cp
	}
	return out
}
