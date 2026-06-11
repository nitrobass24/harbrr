package dateparse

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// relTermRegexp extracts (number, unit) pairs from a "2 hours 1 day" style
// string, mirroring Jackett FromTimeAgo's @"\s*?([\d\.]+)\s*?([^\d\s\.]+)\s*?".
var relTermRegexp = regexp.MustCompile(`([\d.]+)\s*([^\d\s.]+)`)

// isoLayouts are the absolute machine formats tried before relative heuristics,
// covering the RFC3339/ISO 8601 values that appear unfiltered in feeds.
var isoLayouts = []string{
	time.RFC3339,
	time.RFC3339Nano,
	"2006-01-02T15:04:05",
	"2006-01-02 15:04:05",
	"2006-01-02",
	time.RFC1123Z,
	time.RFC1123,
}

// ParseRelTime implements the filter.Registry ParseRelTime seam for the
// timeago/reltime/fuzzytime filters. It returns a canonical RFC3339 string.
//
// Flow (verified against Jackett DateTimeUtil.FromTimeAgo + FromUnknown):
//   - "now"/"just now" -> the reference clock.
//   - unix timestamp (all digits): always seconds (matches Jackett FromUnknown).
//   - ISO 8601 / RFC1123Z absolute strings.
//   - "yesterday"/"today"/"tomorrow [HH:mm]" -> clock date +/- a day.
//   - "N unit(s) ago" (sec/min/hour|hr/day/week|wk/month/year) -> offset from now.
//
// Localized relative terms (e.g. Russian назад/вчера/сегодня) are normalized to
// English first when a language is set, for the corpus's unnormalized feeds.
func (p *Parser) ParseRelTime(value string) (string, error) {
	v := normalizeSpace(value)
	now := p.now()

	if loc, ok := relLocales[primarySubtag(p.lang)]; ok {
		v = applyRelLocale(v, loc)
	}

	lower := strings.ToLower(v)

	if t, ok := parseAbsolute(v, lower, now); ok {
		return t.Format(canonicalLayout), nil
	}
	if t, ok := parseNamedDay(lower, now); ok {
		return t.Format(canonicalLayout), nil
	}
	if t, ok := parseTimeAgo(lower, now); ok {
		return t.Format(canonicalLayout), nil
	}

	return "", fmt.Errorf("%w: relative value %q", ErrUnparseable, value)
}

// parseAbsolute handles "now", unix epochs, and ISO/RFC absolute layouts.
func parseAbsolute(v, lower string, now time.Time) (time.Time, bool) {
	if strings.Contains(lower, "now") {
		return now, true
	}
	if t, ok := parseUnix(v); ok {
		return t, true
	}
	for _, layout := range isoLayouts {
		if t, err := time.Parse(layout, v); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// parseUnix interprets an all-digit value as a unix timestamp in SECONDS.
//
// PARITY: Jackett DateTimeUtil.FromUnknown treats every all-digit string as
// seconds via UnixTimestampToDateTime — there is NO digit-count/magnitude check
// and no milliseconds heuristic. A 13-digit value therefore renders as a
// far-future seconds timestamp, and we match that exactly rather than guessing
// milliseconds (which would produce a different instant than Jackett).
func parseUnix(v string) (time.Time, bool) {
	if v == "" || !isAllDigits(v) {
		return time.Time{}, false
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return time.Time{}, false
	}
	return time.Unix(n, 0).UTC(), true
}

// parseNamedDay handles "today"/"yesterday"/"tomorrow" with an optional trailing
// "HH:mm[:ss]" time component, anchored to the reference clock's date.
func parseNamedDay(lower string, now time.Time) (time.Time, bool) {
	offsets := []struct {
		word string
		days int
	}{
		{"yesterday", -1},
		{"tomorrow", 1},
		{"today", 0},
	}
	for _, o := range offsets {
		if !strings.Contains(lower, o.word) {
			continue
		}
		rest := strings.TrimSpace(strings.Replace(lower, o.word, "", 1))
		dur := parseClockTime(rest)
		day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
		return day.AddDate(0, 0, o.days).Add(dur), true
	}
	return time.Time{}, false
}

// parseClockTime extracts a leading/embedded HH:mm[:ss] from rest as a duration
// since midnight; absent or unparseable yields zero (midnight).
func parseClockTime(rest string) time.Duration {
	rest = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(rest), "at"))
	rest = strings.TrimSpace(rest)
	if rest == "" {
		return 0
	}
	for _, layout := range []string{"15:04:05", "15:04"} {
		if t, err := time.Parse(layout, rest); err == nil {
			return time.Duration(t.Hour())*time.Hour +
				time.Duration(t.Minute())*time.Minute +
				time.Duration(t.Second())*time.Second
		}
	}
	return 0
}

// parseTimeAgo reproduces Jackett FromTimeAgo: strip "ago"/"and"/commas, sum each
// (value, unit) term as a duration, and subtract from now.
func parseTimeAgo(lower string, now time.Time) (time.Time, bool) {
	s := strings.NewReplacer(",", "", "ago", "", "and", "").Replace(lower)
	matches := relTermRegexp.FindAllStringSubmatch(s, -1)
	if len(matches) == 0 {
		return time.Time{}, false
	}
	var total time.Duration
	for _, m := range matches {
		val, err := strconv.ParseFloat(m[1], 64)
		if err != nil {
			return time.Time{}, false
		}
		d, ok := unitDuration(m[2], val)
		if !ok {
			return time.Time{}, false
		}
		total += d
	}
	return now.Add(-total), true
}

// unitDuration maps a unit token + value to a duration, matching Jackett's
// FromTimeAgo substring tests. Sub-day units carry their own base; day-and-up
// units share a day base with a multiplier (week=7, month=30, year=365).
func unitDuration(unit string, val float64) (time.Duration, bool) {
	if base, ok := subDayUnit(unit); ok {
		return time.Duration(val * float64(base)), true
	}
	if mult, ok := dayUnitMultiplier(unit); ok {
		return time.Duration(val * mult * float64(24*time.Hour)), true
	}
	return 0, false
}

// subDayUnit returns the base duration for seconds/minutes/hours units.
func subDayUnit(unit string) (time.Duration, bool) {
	switch {
	case strings.Contains(unit, "sec") || unit == "s":
		return time.Second, true
	case strings.Contains(unit, "min") || unit == "m":
		return time.Minute, true
	case strings.Contains(unit, "hour") || strings.Contains(unit, "hr") || unit == "h":
		return time.Hour, true
	default:
		return 0, false
	}
}

// dayUnitMultiplier returns the day multiplier for day/week/month/year units.
func dayUnitMultiplier(unit string) (float64, bool) {
	switch {
	case strings.Contains(unit, "week") || strings.Contains(unit, "wk") || unit == "w":
		return 7, true
	case strings.Contains(unit, "day") || unit == "d":
		return 1, true
	case strings.Contains(unit, "month") || unit == "mo":
		return 30, true
	case strings.Contains(unit, "year") || unit == "y":
		return 365, true
	default:
		return 0, false
	}
}

// isAllDigits reports whether s is non-empty and every byte is an ASCII digit.
func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// primarySubtag lowercases lang and returns its primary subtag ("ru-RU" -> "ru").
func primarySubtag(lang string) string {
	key := strings.ToLower(lang)
	if i := strings.IndexByte(key, '-'); i > 0 {
		return key[:i]
	}
	return key
}

// relLocale maps localized relative-time terms to their English equivalents so
// the English-based parseTimeAgo/parseNamedDay paths handle them.
type relLocale struct {
	replacements [][2]string
}

// relLocales covers languages whose feeds the corpus does NOT pre-normalize for
// relative times. Russian is the documented case (назад/вчера/сегодня). Order
// matters: longer phrases first.
var relLocales = map[string]relLocale{
	"ru": {replacements: [][2]string{
		{"только что", "now"},
		{"сейчас", "now"},
		{"назад", "ago"},
		{"вчера", "yesterday"},
		{"сегодня", "today"},
		{"завтра", "tomorrow"},
		{"секунд", "sec"},
		{"минут", "min"},
		{"час", "hour"},
		{"дн", "day"},
		{"день", "day"},
		{"недел", "week"},
		{"месяц", "month"},
		{"год", "year"},
		{"лет", "year"},
	}},
}

// applyRelLocale rewrites localized relative terms in v to English, case-folding
// the search so feed casing (Вчера/вчера) is handled uniformly.
func applyRelLocale(v string, loc relLocale) string {
	for _, r := range loc.replacements {
		v = replaceFold(v, r[0], r[1])
	}
	return v
}

// replaceFold replaces all case-insensitive occurrences of old in s with repl.
// Used for relative-term localization, where embedded-substring replacement is
// desired (e.g. "часа" -> match "час"). old is assumed non-empty.
func replaceFold(s, old, repl string) string {
	if old == "" {
		return s
	}
	lowS := strings.ToLower(s)
	lowOld := strings.ToLower(old)
	var b strings.Builder
	for {
		idx := strings.Index(lowS, lowOld)
		if idx < 0 {
			b.WriteString(s)
			break
		}
		b.WriteString(s[:idx])
		b.WriteString(repl)
		s = s[idx+len(old):]
		lowS = lowS[idx+len(lowOld):]
	}
	return b.String()
}
