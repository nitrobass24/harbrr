package filter

import (
	"errors"
	"fmt"
	"html"
	"strconv"
	"strings"

	"golang.org/x/text/unicode/norm"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/encode"
)

// errMissingArg signals a filter invoked without a required argument. Callers
// wrap it with the filter name; the raw arg values are never included.
var errMissingArg = errors.New("missing required argument")

// filterReplace implements replace[old,new]: a replace-all of old with new.
// (Jackett: Data.Replace(Args[0], Args[1]).) Template fragments in the args (e.g. a
// setting-guarded replacement) are evaluated upstream by the field loop's
// renderFilterArgs before the filter runs, so the args reaching here are literal.
func filterReplace(value string, args []string) (string, error) {
	if len(args) < 2 {
		return "", fmt.Errorf("replace needs 2 args, got %d: %w", len(args), errMissingArg)
	}
	return strings.ReplaceAll(value, args[0], args[1]), nil
}

// filterAppend implements append[s]: append s to the value.
func filterAppend(value string, args []string) (string, error) {
	return value + firstArg(args), nil
}

// filterPrepend implements prepend[s]: prepend s to the value.
func filterPrepend(value string, args []string) (string, error) {
	return firstArg(args) + value, nil
}

// filterTrim implements trim[chars?]. With an argument, Jackett trims the FIRST
// rune of the cutset from both ends (Data.Trim(cutset[0])); with no argument it
// strips leading/trailing whitespace.
func filterTrim(value string, args []string) (string, error) {
	if len(args) == 0 || args[0] == "" {
		return strings.TrimSpace(value), nil
	}
	cut := firstRune(args[0])
	return strings.Trim(value, cut), nil
}

// firstRune returns the first rune of s as a string. Jackett indexes cutset[0]
// as a UTF-16 char; for the ASCII trim characters used in definitions this is
// equivalent to the first rune.
func firstRune(s string) string {
	for _, r := range s {
		return string(r)
	}
	return ""
}

// filterToLower implements tolower.
func filterToLower(value string, _ []string) (string, error) {
	return strings.ToLower(value), nil
}

// filterToUpper implements toupper.
func filterToUpper(value string, _ []string) (string, error) {
	return strings.ToUpper(value), nil
}

// filterURLDecode implements urldecode, matching Jackett's
// WebUtilityHelpers.UrlDecode (.NET WebUtility.UrlDecode). Unlike Go's
// url.QueryUnescape, .NET never throws: invalid or incomplete percent escapes
// stay literal. Values reaching this filter are often already-decoded titles
// (e.g. querystring then urldecode chains), where a bare '%' in a release name
// would otherwise error and drop the whole row.
func filterURLDecode(value string, _ []string) (string, error) {
	return webUtilityURLDecode(value), nil
}

// webUtilityURLDecode mirrors .NET System.Net.WebUtility.UrlDecode: '+'
// becomes a space, a valid %XX escape (case-insensitive hex) decodes to its
// byte, and anything malformed — a trailing '%', or '%' not followed by two
// hex digits — is emitted verbatim. Decoded bytes are collected first and the
// buffer interpreted as UTF-8, so multi-byte escapes like %E2%80%A6 reassemble
// into a single rune.
func webUtilityURLDecode(s string) string {
	buf := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '+':
			buf = append(buf, ' ')
		case c == '%' && i+2 < len(s):
			hi, okHi := unhex(s[i+1])
			lo, okLo := unhex(s[i+2])
			if okHi && okLo {
				buf = append(buf, hi<<4|lo)
				i += 2
				continue
			}
			buf = append(buf, c)
		default:
			buf = append(buf, c)
		}
	}
	return string(buf)
}

// unhex returns the value of a hex digit byte and whether c is one.
func unhex(c byte) (byte, bool) {
	switch {
	case '0' <= c && c <= '9':
		return c - '0', true
	case 'a' <= c && c <= 'f':
		return c - 'a' + 10, true
	case 'A' <= c && c <= 'F':
		return c - 'A' + 10, true
	default:
		return 0, false
	}
}

// filterURLEncode implements the urlencode filter. Jackett applies
// WebUtilityHelpers.UrlEncode (= .NET WebUtility.UrlEncode, space -> '+'); see
// the encode package for the exact .NET-compatible character set.
func filterURLEncode(value string, _ []string) (string, error) {
	return encode.WebUtilityEncode(value), nil
}

// filterHTMLDecode implements htmldecode (WebUtility.HtmlDecode).
func filterHTMLDecode(value string, _ []string) (string, error) {
	return html.UnescapeString(value), nil
}

// filterHTMLEncode implements htmlencode via a .NET WebUtility.HtmlEncode-faithful
// encoder (the encoder Jackett's htmlencode filter uses). Go's html.EscapeString
// diverges — it emits &#34; for '"' where .NET emits &quot;, and it never encodes
// non-ASCII — so it is not used here.
func filterHTMLEncode(value string, _ []string) (string, error) {
	return webUtilityHTMLEncode(value), nil
}

// webUtilityHTMLEncode mirrors .NET System.Net.WebUtility.HtmlEncode: the five HTML
// specials become entities (&lt; &gt; &amp; &quot; &#39;), the Latin-1 supplement
// (U+00A0–U+00FF) and astral code points (>= U+10000) become DECIMAL numeric
// character references, and everything else — including U+007F–U+009F and
// U+0100–U+FFFF — passes through unescaped.
func webUtilityHTMLEncode(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r == '<':
			b.WriteString("&lt;")
		case r == '>':
			b.WriteString("&gt;")
		case r == '&':
			b.WriteString("&amp;")
		case r == '"':
			b.WriteString("&quot;")
		case r == '\'':
			b.WriteString("&#39;")
		case (r >= 0xA0 && r <= 0xFF) || r >= 0x10000:
			b.WriteString("&#")
			b.WriteString(strconv.Itoa(int(r)))
			b.WriteByte(';')
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// filterPassthrough implements hexdump/strdump: Jackett only debug-logs and
// leaves Data unchanged, so the value passes through untouched.
func filterPassthrough(value string, _ []string) (string, error) {
	return value, nil
}

// filterSplit implements split[sep,index]: split on the first rune of sep and
// return the element at index. A negative index counts from the end
// (index += len). Out-of-range is an error (Jackett indexes the array directly,
// which throws).
func filterSplit(value string, args []string) (string, error) {
	if len(args) < 2 {
		return "", fmt.Errorf("split needs 2 args, got %d: %w", len(args), errMissingArg)
	}
	sep := firstRune(args[0])
	if sep == "" {
		return "", fmt.Errorf("split: empty separator: %w", errMissingArg)
	}
	pos, err := strconv.Atoi(args[1])
	if err != nil {
		return "", fmt.Errorf("split: index not an integer: %w", err)
	}
	parts := strings.Split(value, sep)
	if pos < 0 {
		pos += len(parts)
	}
	if pos < 0 || pos >= len(parts) {
		return "", fmt.Errorf("split: index %d out of range for %d parts", pos, len(parts))
	}
	return parts[pos], nil
}

// filterQueryString implements querystring[param]: return the named query
// parameter (first occurrence) from the value's query component, matching
// Jackett's ParseUtil.GetArgumentFromQueryString — split on the first '?', drop
// a '#' fragment, then parse. A missing '?' is an error (Jackett's Split('?')[1]
// would throw). The parse itself is done by queryStringFirst, which mirrors
// QueryHelpers.ParseQuery and, unlike Go's url.ParseQuery, never errors — so a
// download href with one malformed sibling param (a bare '%' or a ';') still
// yields the target value instead of dropping the whole row.
func filterQueryString(value string, args []string) (string, error) {
	param := firstArg(args)
	if param == "" {
		return "", fmt.Errorf("querystring: %w", errMissingArg)
	}
	_, after, found := strings.Cut(value, "?")
	if !found {
		return "", errors.New("querystring: value has no query component")
	}
	qsStr, _, _ := strings.Cut(after, "#")
	return queryStringFirst(qsStr, param), nil
}

// queryStringFirst mirrors .NET QueryHelpers.ParseQuery followed by
// qs[param].FirstOrDefault(): split the query on '&' ONLY (a ';' is ordinary
// data, never a separator — the divergence that makes Go's url.ParseQuery drop
// ';'-bearing pairs), split each pair on its first '=', lenient-decode key and
// value, and return the value of the first pair whose decoded key equals param.
// An absent param yields "" (FirstOrDefault on an empty collection).
//
// Decoding: Jackett runs each key/value through ReplacePlusWithSpace then
// Uri.UnescapeDataString ('+' -> space, malformed '%' left literal, never
// throws). webUtilityURLDecode (added for the urldecode filter, U2-F3) mirrors
// .NET WebUtility.UrlDecode and produces the identical result for every input
// here — both map '+' to space and leave an invalid '%' escape literal; they
// diverge only on lone invalid-UTF-8 percent bytes, which do not occur in real
// download hrefs — so it is reused rather than duplicated.
func queryStringFirst(qs, param string) string {
	for qs != "" {
		var pair string
		pair, qs, _ = strings.Cut(qs, "&")
		key, val, _ := strings.Cut(pair, "=")
		if webUtilityURLDecode(key) == param {
			return webUtilityURLDecode(val)
		}
	}
	return ""
}

// filterValidFilename implements validfilename: replace filesystem-invalid
// characters with '_'. Jackett calls MakeValidFileName(Data, '_', false), which
// uses Path.GetInvalidFileNameChars() — a host-OS-DEPENDENT .NET API. We
// deliberately mirror the Windows set (control chars 0–31 plus reserved
// punctuation), the stricter and deterministic choice. Strict parity with
// Jackett-on-Linux (its primary Docker deployment) would strip only '/' and NUL;
// the divergence is latent (no corpus def uses validfilename today). An
// all-invalid result collapses to "_".
func filterValidFilename(value string, _ []string) (string, error) {
	var sb strings.Builder
	sb.Grow(len(value))
	changed := false
	for _, r := range value {
		if isInvalidFilenameRune(r) {
			changed = true
			sb.WriteByte('_')
			continue
		}
		sb.WriteRune(r)
	}
	if sb.Len() == 0 {
		return "_", nil
	}
	if !changed {
		return value, nil
	}
	return sb.String(), nil
}

// isInvalidFilenameRune reports whether r is in .NET's Windows
// Path.GetInvalidFileNameChars() set: control chars 0–31 plus the reserved
// punctuation. This intentionally mirrors the WINDOWS set (not the Linux set,
// which is only {'\0','/'}) so behavior is deterministic across harbrr hosts.
func isInvalidFilenameRune(r rune) bool {
	if r < 0x20 {
		return true
	}
	switch r {
	case '"', '<', '>', '|', ':', '*', '?', '\\', '/':
		return true
	default:
		return false
	}
}

// filterDiacritics implements diacritics[replace]: NFD-decompose, drop
// non-spacing marks, recompose NFC. Jackett only supports the "replace"
// argument and throws otherwise.
func filterDiacritics(value string, args []string) (string, error) {
	if firstArg(args) != "replace" {
		return "", errors.New("diacritics: unsupported argument (only \"replace\")")
	}
	decomposed := norm.NFD.String(value)
	var sb strings.Builder
	sb.Grow(len(decomposed))
	for _, r := range decomposed {
		if isNonSpacingMark(r) {
			continue
		}
		sb.WriteRune(r)
	}
	return norm.NFC.String(sb.String()), nil
}
