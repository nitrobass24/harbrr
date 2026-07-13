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

// stringFormLiterals restores the four sub-delimiters that .NET's
// WebUtility.UrlEncode leaves LITERAL in its output STRING (safe set
// A-Za-z0-9-_.!*()), reversing the percent-encoding WebUtilityEncode applies for
// the on-the-wire form. A "%21"/"%2A"/"%28"/"%29" run in WebUtilityEncode output
// can only have come from a literal ! * ( ) in the input (a literal '%' becomes
// "%25"), so this rewrite is unambiguous.
var stringFormLiterals = strings.NewReplacer(
	"%21", "!",
	"%2A", "*",
	"%28", "(",
	"%29", ")",
)

// WebUtilityStringEncode reproduces the intermediate STRING that .NET's
// WebUtility.UrlEncode produces (via WebUtility.UrlEncodeToBytes): identical to
// WebUtilityEncode except the sub-delimiters ! * ( ) are left LITERAL rather than
// percent-encoded. space -> '+', ~ -> %7E, ' -> %27, Unicode -> UTF-8 octets, all
// as in WebUtilityEncode.
//
// This is the form Jackett emits into magnet-link dn=/tr= values — MagnetUtil.
// InfoHashToPublicMagnet builds them via WebUtilityHelpers.UrlEncode ->
// WebUtility.UrlEncodeToBytes, whose safe set includes ! * ( ). It is NOT for
// on-the-wire request URLs: those use WebUtilityEncode, which percent-encodes
// ! * ( ) to match .NET HttpClient's actual wire serialization and to dodge some
// trackers' Cloudflare/WAF paren heuristics (see the package doc). A synthesised
// magnet is Torznab OUTPUT, not a tracker request, so it must match Jackett's
// STRING form.
//
// Caveat: Jackett ultimately emits r.MagnetUri.AbsoluteUri (ResultPage.ToXml),
// i.e. new Uri(this-string).AbsoluteUri. That round-trip is a no-op for the
// sub-delimiters ! * ( ) (valid unescaped in a query, kept literal) — which is
// the byte-diff this encoder fixes — but AbsoluteUri unescapes RFC-3986
// UNRESERVED chars, notably ~ (%7E -> ~), at least for hierarchical URIs. Whether
// the magnet: OPAQUE scheme unescapes ~ is unverified here (no .NET available), so
// the ~ -> %7E emission is pending a live differential diff; see the U6-F4
// follow-up. Every corpus/synthesised title today is ~-free, so this is latent.
func WebUtilityStringEncode(s string) string {
	return stringFormLiterals.Replace(WebUtilityEncode(s))
}
