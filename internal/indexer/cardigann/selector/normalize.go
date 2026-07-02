package selector

import "strings"

// normalizeSpace reproduces Jackett's ParseUtil.NormalizeSpace, which is applied
// to every extracted value (HTML innerText, HTML attribute, JSON leaf, case, and
// text) before the filter chain.
//
// NormalizeSpace is TRIM-ONLY: `s?.Trim() ?? ""`. It does NOT collapse internal
// whitespace runs — that is the separate NormalizeMultiSpaces, which
// handleSelector/handleJsonSelector do not call. AngleSharp's TextContent (the
// HTML value source) preserves source whitespace verbatim, exactly like
// goquery's Text(), so a plain trim of goquery's text matches Jackett's
// observable output. Collapsing here would silently corrupt values that carry
// significant internal whitespace.
func normalizeSpace(s string) string {
	return strings.TrimSpace(s)
}
