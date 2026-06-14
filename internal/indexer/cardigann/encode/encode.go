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
// WebUtility's unreserved (left-literal) set is, per the dotnet/runtime source
// (s_safeUrlChars):
//
//	A-Z a-z 0-9 - _ . ! * ( )
//
// Go's net/url.QueryEscape uses a different unreserved set (A-Z a-z 0-9 - _ . ~),
// so for a query component the two differ on exactly five characters:
//
//	! * ( )   Go percent-escapes these; .NET leaves them literal.
//	~         Go leaves this literal; .NET percent-escapes it (%7E).
//
// Note that the apostrophe (') is percent-escaped (%27) by BOTH engines — it is
// NOT a divergence (a common misconception; harbrr's earlier parity note and the
// Phase 5 plan both wrongly listed it and omitted '~'). The five-character set
// above is the complete divergence, verified against the dotnet/runtime
// WebUtility source and Jackett's WebUtilityHelpers/StringUtil. Unicode is
// percent-escaped as UTF-8 octets identically by both engines.
package encode

import (
	"net/url"
	"strings"
)

// WebUtilityEncode encodes s the way .NET WebUtility.UrlEncode does: space -> '+',
// the sub-delimiters ! * ( ) left literal, and ~ percent-escaped. It is the
// query-component encoder (matches Jackett's GetQueryString value encoding).
func WebUtilityEncode(s string) string {
	s = url.QueryEscape(s)
	// url.QueryEscape escaped ! * ( ) that .NET leaves literal — unescape them.
	s = strings.ReplaceAll(s, "%21", "!")
	s = strings.ReplaceAll(s, "%2A", "*")
	s = strings.ReplaceAll(s, "%28", "(")
	s = strings.ReplaceAll(s, "%29", ")")
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
