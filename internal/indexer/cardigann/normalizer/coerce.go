package normalizer

import (
	"strconv"
	"strings"
)

// coerceLong reproduces Jackett's ParseUtil.CoerceLong: keep only digits, '.'
// and ',', map a leading '-' run to zero, treat thousands separators as noise,
// and parse the integer part. Jackett feeds the .NET parser NumberStyles.Any
// with InvariantCulture; for the integer fields harbrr models (seeders,
// leechers, files, grabs, year, minimumseedtime) only the integer magnitude is
// meaningful, so we drop any fractional tail rather than reject the value.
// Unparseable input coerces to 0 (lenient), matching Jackett's "-" -> 0 cases.
func coerceLong(s string) int64 {
	// Jackett's CoerceLong feeds NormalizeNumber(isInt:true) to a .NET parse with
	// NumberStyles.Any: every '.'/',' is treated as a thousands group separator,
	// so the integer value is the concatenation of all digits. (The lone "both
	// separators present" branch in Jackett produces a parse that still groups;
	// for the integer fields harbrr models this collapses to the same result.)
	// A bare '-'/'---' yields no digits -> 0, matching Jackett's documented case.
	intPart := stripSeparators(keepNumeric(s))
	if intPart == "" {
		return 0
	}
	n, err := strconv.ParseInt(intPart, 10, 64)
	if err != nil {
		return 0
	}
	return n
}

// coerceDouble reproduces Jackett's ParseUtil.CoerceDouble/NormalizeNumber for
// the float fields (downloadvolumefactor, uploadvolumefactor, minimumratio):
// keep digits/'.'/',', map ',' to '.', and when several '.' remain treat all
// but the last as thousands separators. Unparseable input coerces to 0.
func coerceDouble(s string) float64 {
	norm := normalizeDecimal(keepNumeric(s))
	if norm == "" {
		return 0
	}
	f, err := strconv.ParseFloat(norm, 64)
	if err != nil {
		return 0
	}
	return f
}

// firstIntRun reproduces Jackett's ParseUtil.GetLongFromString: scan for the
// first contiguous run of ASCII digits and parse it, ignoring everything else.
// Used for the external-id fields (imdb, tmdbid, tvdbid, ...). No digits -> 0,
// mirroring GetValueOrDefault() on the nullable result.
func firstIntRun(s string) int64 {
	start, end := -1, -1
	for i := 0; i < len(s); i++ {
		if s[i] >= '0' && s[i] <= '9' {
			if start < 0 {
				start = i
			}
			end = i + 1
			continue
		}
		if start >= 0 {
			break
		}
	}
	if start < 0 {
		return 0
	}
	n, err := strconv.ParseInt(s[start:end], 10, 64)
	if err != nil {
		return 0
	}
	return n
}

// keepNumeric retains only digits, '.' and ',' then strips '-' (Jackett maps it
// to '0' before parsing; for our integer/float extraction dropping it yields the
// same magnitude, and the all-separator case is handled by the callers).
func keepNumeric(s string) string {
	var b strings.Builder
	for _, r := range s {
		if (r >= '0' && r <= '9') || r == '.' || r == ',' {
			b.WriteRune(r)
		}
	}
	return strings.ReplaceAll(b.String(), "-", "")
}

// stripSeparators drops every '.'/',' grouping separator, leaving bare digits.
func stripSeparators(s string) string {
	return strings.NewReplacer(".", "", ",", "").Replace(s)
}

// normalizeDecimal mirrors NormalizeNumber's float branch: ',' -> '.', and when
// multiple '.' remain, all but the last are thousands separators and removed.
func normalizeDecimal(s string) string {
	s = strings.ReplaceAll(s, ",", ".")
	if strings.Count(s, ".") <= 1 {
		return s
	}
	last := strings.LastIndexByte(s, '.')
	return strings.ReplaceAll(s[:last], ".", "") + s[last:]
}
