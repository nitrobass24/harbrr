package filter

import (
	"errors"
	"fmt"
	"html"
	"net/url"
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

// filterURLDecode implements urldecode via net/url query unescaping, matching
// Jackett's WebUtilityHelpers.UrlDecode (which treats '+' as a space).
func filterURLDecode(value string, _ []string) (string, error) {
	out, err := url.QueryUnescape(value)
	if err != nil {
		return "", fmt.Errorf("urldecode: %w", err)
	}
	return out, nil
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

// filterHTMLEncode implements htmlencode (WebUtility.HtmlEncode).
func filterHTMLEncode(value string, _ []string) (string, error) {
	return html.EscapeString(value), nil
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

// filterQueryString implements querystring[param]: parse the value as a URL and
// return the named query parameter (first occurrence), matching Jackett's
// GetArgumentFromQueryString — split on '?', drop the '#' fragment, parse.
// A missing '?' is an error (Jackett's split[1] would throw).
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
	qs, err := url.ParseQuery(qsStr)
	if err != nil {
		return "", fmt.Errorf("querystring: parsing query: %w", err)
	}
	// FirstOrDefault: absent param yields the empty string.
	return qs.Get(param), nil
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
