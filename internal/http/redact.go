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

// secretNameAlternation is the SINGLE shared vocabulary of credential-shaped
// key/parameter/header names. Every name-based redaction surface (URL query
// params via RedactURL, HTTP headers via RedactHeader, JSON object keys via
// RedactJSONBody, and the credential key=value / "key":value scrubs in
// RedactError) derives its matcher from this one alternation, so the three
// formerly-drifted lists can no longer diverge. It is intentionally broad —
// Cardigann definitions and trackers spell these many ways — and matches the
// `_`/`-`/none separator variants (api_key, api-key, x-api-key) in one go.
// Longer/more-specific tokens precede their shorter prefixes so the key-capturing
// regexes group the whole word (e.g. "password" before "pass", "downloadtoken"
// before "token", "api[_-]?key" before bare "key").
const secretNameAlternation = `passphrase|passkey|passid|password|x[_-]?api[_-]?key|api[_-]?key|auth[_-]?key|rss[_-]?key|torrent[_-]?pass|downloadtoken|cf[_-]?clearance|secret|token|cookie|2fa|otp|auth|key|pass|pid`

// secretNameRe matches (case-insensitively, anywhere in the name) any credential
// name token. Used as the boolean "is this name a secret?" test for query
// parameters, headers, and JSON keys so all three share one vocabulary.
var secretNameRe = regexp.MustCompile(`(?i)(?:` + secretNameAlternation + `)`)

// flaresolverrSecretKeys are JSON keys that are NOT credential-named but still carry
// secrets/PII in the FlareSolverr /v1 request/response shape, so RedactJSONBody scrubs
// them on top of the shared credential vocabulary. (cookie/cookies/set-cookie and
// cf_clearance are already caught by secretNameRe.)
var flaresolverrSecretKeys = map[string]struct{}{
	"postdata": {}, "useragent": {}, "user-agent": {},
	// FlareSolverr solution fields: the raw page HTML (may embed session tokens) and
	// the response header map (Set-Cookie etc.) are redacted wholesale, and the
	// request "proxy" field may embed user:pass.
	"response": {}, "headers": {}, "proxy": {},
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
	if p := redactPathSecrets(u.Path); p != u.Path {
		// A passkey/apikey/rsskey can ride in a PATH segment (animebytes, beyondhd),
		// which the query-only scrub below cannot reach. Setting Path and clearing
		// RawPath makes String() re-encode the redacted (canonical) path.
		u.Path, u.RawPath = p, ""
	}
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

// RedactURLIdentity is RedactURL for a URL used as a stable IDENTITY — a dedup-key
// <guid> or a details permalink — rather than a credential vector. It scrubs userinfo
// passwords and secret query-parameter values but preserves the PATH verbatim.
//
// RedactURL redacts long hex/alphanumeric path tokens as possible passkeys
// (redactPathSecrets); on an identity URL that is wrong, because a release's id is
// often exactly such a token — e.g. a Newznab <guid> like
// https://host/details/<32-hex>. Redacting it collapses every release to the same
// string, so a downstream dedup-by-guid keeps only one of them. This variant leaves
// that id intact so the identity stays distinct, while still scrubbing a genuine
// secret carried in a query param or userinfo. Use it ONLY where the path is a public
// identifier, never where the path itself can carry a credential.
func RedactURLIdentity(raw string) string {
	if raw == "" {
		return raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return redactURLIdentityFallback(raw)
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

// redactURLIdentityFallback handles input url.Parse rejects for RedactURLIdentity: it
// scrubs a userinfo password and strips the query string (which may carry a secret) but
// keeps the path verbatim, so the identity is preserved even when the URL is malformed.
func redactURLIdentityFallback(raw string) string {
	raw = rawUserinfoRe.ReplaceAllString(raw, `${1}`+redactedValue+`${2}`)
	if path, _, found := strings.Cut(raw, "?"); found {
		return path + "?" + redactedValue
	}
	return raw
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

// rawUserinfoRe matches a userinfo password in a raw URL string (`//user:PASS@`).
// The username char class excludes ':' so group 1 stops at the FIRST colon,
// redacting the whole password even when it contains colons.
var rawUserinfoRe = regexp.MustCompile(`(//[^/?#@:]*:)[^/?#@]*(@)`)

// redactURLFallback handles input url.Parse rejects. Rather than risk emitting a
// raw secret, it scrubs any userinfo password (redactUserinfo only runs on a parsed
// URL), strips the entire query string (everything after the first '?') and appends
// a marker, and scrubs any secret-shaped PATH token from the kept structural prefix.
func redactURLFallback(raw string) string {
	raw = rawUserinfoRe.ReplaceAllString(raw, `${1}`+redactedValue+`${2}`)
	if path, _, found := strings.Cut(raw, "?"); found {
		return redactPathSecrets(path) + "?" + redactedValue
	}
	return redactPathSecrets(raw)
}

// isSecretParam reports whether a query-parameter name should have its value
// redacted, using the shared credential-name vocabulary (case-insensitive,
// substring) so the many tracker spellings (torrent_pass, rsskey, api_key, ...)
// are all caught.
func isSecretParam(name string) bool {
	return secretNameRe.MatchString(name)
}

// pathSecretRe matches a credential-shaped token embedded in a URL path: a long
// run of hex (passkeys/apikeys/infohashes are typically 32–40 hex chars) or a long
// alphanumeric token. Short, structural path segments (api, indexers, results, a slug)
// never reach the threshold, so legitimate paths are untouched.
var pathSecretRe = regexp.MustCompile(`(?i)[0-9a-f]{32,}|[a-z0-9]{40,}`)

// redactPathSecrets replaces every secret-shaped token in a URL path with the
// placeholder. RedactURL is otherwise query-only, so a passkey/apikey carried in a
// path segment (animebytes, beyondhd) would survive without this.
func redactPathSecrets(path string) string {
	return pathSecretRe.ReplaceAllString(path, redactedValue)
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
		// One shared vocabulary: catches Authorization, Cookie/Set-Cookie,
		// Proxy-Authorization, and every api-key spelling (X-Api-Key, Api-Key,
		// x-api-key) without a per-name allowlist that could drift.
		if secretNameRe.MatchString(name) {
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
// URL query) so the value can be scrubbed from an error message. The key alternation
// is the shared secretNameAlternation, so it can never drift from the URL/header/JSON
// surfaces. The value run stops at whitespace and the URL/quote delimiters & " ' so
// surrounding context (e.g. "dial tcp") and other query params survive.
var secretTokenRe = regexp.MustCompile(`(?i)(` + secretNameAlternation + `)([=:]\s*)[^\s&"']+`)

// jsonSecretRe scrubs a credential-shaped key's value in a JSON object —
// `"apiKey":"…"` / `"password": "…"`. secretTokenRe misses these because the quote
// before the `:` breaks its `key[=:]` anchor, so an app's JSON error body (echoed by
// app-sync) could otherwise leak the value verbatim. It shares secretNameAlternation
// with every other surface. The value run `(?:\\.|[^"\\])*` consumes escape sequences
// (`\"`, `\\`) so an embedded escaped quote can't end the match early and leak the tail.
var jsonSecretRe = regexp.MustCompile(`(?i)("(?:` + secretNameAlternation + `)"\s*:\s*)"(?:\\.|[^"\\])*"`)

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
	msg = jsonSecretRe.ReplaceAllString(msg, `${1}"<redacted>"`)
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

// isSecretJSONKey reports whether a JSON object key's value must be redacted: any
// credential-shaped name (the shared secretNameRe vocabulary — cookie, apikey,
// rsskey, authkey, torrent_pass, ...) or a FlareSolverr structural key that carries
// secrets/PII without a credential-shaped name (postData, userAgent, response, ...).
func isSecretJSONKey(k string) bool {
	if _, ok := flaresolverrSecretKeys[strings.ToLower(k)]; ok {
		return true
	}
	return secretNameRe.MatchString(k)
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
			if isSecretJSONKey(k) {
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
