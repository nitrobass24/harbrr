// Package encode provides URL value encoders that match the .NET
// System.Net.WebUtility.UrlEncode semantics Jackett uses when building tracker
// requests, so harbrr produces byte-identical request URLs.
//
// Jackett encodes both halves of a search request with WebUtility.UrlEncode:
//   - GET query values go through StringUtil.GetQueryString -> WebUtilityHelpers.UrlEncode
//     -> WebUtility.UrlEncodeToBytes (space -> '+').
//   - Search-path template values go through applyGoTemplateText(..., WebUtility.UrlEncode)
//     followed by .Replace("+", "%20") (space -> '%20').
//
// WebUtility's unreserved (left-literal) STRING set is, per the dotnet/runtime
// source (s_safeUrlChars): A-Z a-z 0-9 - _ . ! * ( ). That is the intermediate
// STRING .NET produces — but it is NOT what goes on the wire. .NET's HttpClient
// percent-encodes the sub-delimiters ! * ( ) when it serializes the request URI,
// and a LITERAL '(' in a query trips some trackers' Cloudflare/WAF injection
// heuristics (HD-Space 500s a "Title (Year)" search with a literal paren, while
// Prowlarr — whose effective request is %28 — returns results). harbrr therefore
// matches the on-the-wire form: it percent-encodes ! * ( ) (as Go's
// url.QueryEscape already does, and as harbrr's own login path does via
// url.Values), and percent-escapes ~ to %7E (the one char .NET escapes that Go
// leaves literal). Net divergence from Go's url.QueryEscape: only '~'. The
// apostrophe (') is %27 in both. Unicode is percent-escaped as UTF-8 octets
// identically. The parity corpus carries no request URL with these characters, so
// this is a live-correctness fix, not a parity-corpus change.
package encode

import (
	"net/url"
	"strings"
)

// WebUtilityEncode encodes s for a URL query component: Go's url.QueryEscape
// (space -> '+', sub-delimiters ! * ( ) percent-escaped — the on-the-wire form)
// plus ~ -> %7E to match .NET. See the package doc for why the sub-delimiters are
// NOT left literal (tracker WAF compatibility).
func WebUtilityEncode(s string) string {
	s = url.QueryEscape(s)
	// url.QueryEscape left ~ literal; .NET escapes it. Percent sequences never
	// contain a literal '~', so this only rewrites genuine tildes from the input.
	s = strings.ReplaceAll(s, "~", "%7E")
	return s
}

// PathEscape encodes s for substitution into a search path: WebUtilityEncode
// then '+' -> "%20" (matching Jackett's WebUtility.UrlEncode + Replace("+","%20")).
// Only spaces appear as '+' in WebUtilityEncode output — a literal '+' in the
// input is already escaped to %2B — so this rewrites spaces only.
func PathEscape(s string) string {
	return strings.ReplaceAll(WebUtilityEncode(s), "+", "%20")
}
