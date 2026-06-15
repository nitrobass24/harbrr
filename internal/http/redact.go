package http

import (
	"encoding/json"
	"net/http"
	"net/url"
	"regexp"
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

// redactURLFallback handles input url.Parse rejects. Rather than risk emitting a
// raw secret, it strips the entire query string (everything after the first
// '?') and appends a marker, keeping only the structural prefix.
func redactURLFallback(raw string) string {
	if i := strings.IndexByte(raw, '?'); i >= 0 {
		return raw[:i] + "?" + redactedValue
	}
	return raw
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

// secretTokenRe matches a credential-shaped key and its value (plain text or in a
// URL query) so the value can be scrubbed from an error message. The value run
// stops at whitespace and the URL/quote delimiters & " ' so surrounding context
// (e.g. "dial tcp") and other query params survive.
var secretTokenRe = regexp.MustCompile(`(?i)(cookie|passkey|api_?key|auth_?key|rss_?key|torrent_pass|passid|passphrase|password|secret|token|downloadtoken|2fa|otp)([=:]\s*)[^\s&"']+`)

// authHeaderRe scrubs an Authorization header value (with or without a scheme like
// Bearer/Basic), since the scheme + token can span a space the value run above
// would not cover.
var authHeaderRe = regexp.MustCompile(`(?i)(authorization)(\s*[=:]\s*)(?:bearer|basic|digest|negotiate)?\s*\S+`)

// cookieHeaderRe scrubs an ENTIRE Cookie/Set-Cookie value (multiple ";"-separated
// pairs, to end of line). The whole header value is sensitive, so secretTokenRe's
// single-token run is not enough — it would stop at the first whitespace and leak
// later pairs (e.g. "Cookie: a=1; cf_clearance=SECRET").
var cookieHeaderRe = regexp.MustCompile(`(?i)((?:set-)?cookie)(\s*[=:]\s*)[^\r\n]+`)

// RedactError renders an error message safe to surface (to an API client or a
// persisted health-event detail): the full Cookie/Set-Cookie/Authorization header
// value, and every other credential-shaped key=value / key: value pair, are
// replaced with <redacted>. It is the shared scrubbing chokepoint — the engine's
// errors are credential-free by construction and URLs are RedactURL'd at their
// sites, but a credential must never reach these surfaces. A nil error returns "".
func RedactError(err error) string {
	if err == nil {
		return ""
	}
	msg := authHeaderRe.ReplaceAllString(err.Error(), "${1}${2}<redacted>")
	msg = cookieHeaderRe.ReplaceAllString(msg, "${1}${2}<redacted>")
	return secretTokenRe.ReplaceAllString(msg, "${1}${2}<redacted>")
}

// RedactProxyURL is RedactURL for a proxy URL: it scrubs the WHOLE userinfo
// (username AND password — a proxy username can itself be an account id), not just
// the password as RedactURL does, plus any secret query parameters. An unparseable
// URL falls back to RedactURL's own unparseable handling.
func RedactProxyURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		// A malformed proxy URL can still carry userinfo before the parse error;
		// RedactURL's textual fallback would keep that prefix, so return a fixed
		// marker rather than risk leaking proxy credentials.
		return redactedValue
	}
	if u.User != nil {
		u.User = url.User(redactedValue)
	}
	return RedactURL(u.String())
}

// jsonSecretKeys are JSON object keys whose values are credentials/PII and are
// redacted wholesale by RedactJSONBody (case-insensitive). Covers the FlareSolverr
// /v1 request/response shape (cookies, postData, userAgent) plus the cf_clearance
// a solution carries and the standard auth headers.
var jsonSecretKeys = map[string]struct{}{
	"cookie": {}, "cookies": {}, "set-cookie": {},
	"postdata": {}, "useragent": {}, "user-agent": {}, "cf_clearance": {},
	"authorization": {}, "proxy-authorization": {},
	"token": {}, "apikey": {}, "api_key": {}, "passkey": {}, "password": {}, "secret": {},
	// FlareSolverr solution fields: the raw page HTML (may embed session tokens) and
	// the response header map (Set-Cookie etc.) are redacted wholesale, and the
	// request "proxy" field may embed user:pass.
	"response": {}, "headers": {}, "proxy": {},
}

// RedactJSONBody returns body with the values of any credential-shaped keys (at any
// nesting depth) replaced by a placeholder, so a FlareSolverr /v1 request/response
// body can be logged safely (RedactURL/RedactHeader cannot reach JSON). A body that
// is not valid JSON is replaced wholesale rather than risk leaking it raw.
func RedactJSONBody(body []byte) []byte {
	var v any
	if err := json.Unmarshal(body, &v); err != nil {
		return []byte(`"` + redactedValue + `"`)
	}
	out, err := json.Marshal(scrubJSON(v))
	if err != nil {
		return []byte(`"` + redactedValue + `"`)
	}
	return out
}

// scrubJSON recursively replaces the value of any secret-named key with the
// placeholder, recursing into nested objects/arrays otherwise.
func scrubJSON(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, val := range t {
			if _, secret := jsonSecretKeys[strings.ToLower(k)]; secret {
				out[k] = redactedValue
				continue
			}
			out[k] = scrubJSON(val)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, val := range t {
			out[i] = scrubJSON(val)
		}
		return out
	default:
		return v
	}
}
