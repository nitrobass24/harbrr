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
	"2006-01-02 15:04",
	"2006-01-02",
	// Day-lenient RFC1123Z/RFC1123 equivalents: Go's "2" day token accepts both
	// zero-padded and single-digit days, subsuming the strict "02" forms below.
	// MTV (and other feeds) emit unpadded days for 1-9 (e.g. "Fri, 1 Oct 2021
	// 10:05:54 +0000"), which time.RFC1123Z/RFC1123 reject outright.
	"Mon, 2 Jan 2006 15:04:05 -0700",
	"Mon, 2 Jan 2006 15:04:05 MST",
}

// humanLayouts are the human-readable absolute formats Jackett's
// DateTimeUtil.FromUnknown reaches via its final FromFuzzyTime fallback — the
// DateTimeRoutines natural-language parser, which is MORE permissive than
// DateTime.Parse. We cannot port that parser; this finite layout set
// approximates it for the realistic feed forms (month-name dates with an
// optional time, invariant slash dates). Anything beyond these layouts still
// fails ParseRelTime — the same row-drop failure mode as before, for a smaller
// set of inputs. Only unambiguous and invariant (MM/dd/yyyy) forms are
// included, to avoid guessing on dd/MM ambiguity.
var humanLayouts = []string{
	"Jan 2, 2006",
	"Jan 2 2006",
	"Jan 2, 2006 15:04",
	"Jan 2, 2006 15:04:05",
	"Jan 2, 2006 3:04 PM",
	"January 2, 2006",
	"January 2 2006",
	"January 2, 2006 15:04",
	"2 Jan 2006",
	"02 Jan 2006",
	"2 Jan 2006 15:04",
	"2 Jan 2006 15:04:05",
	"2 January 2006",
	"02 January 2006",
	"2 January 2006 15:04",
	"01/02/2006",
	"01/02/2006 15:04",
	"01/02/2006 15:04:05",
	"2006/01/02",
	"2006/01/02 15:04:05",
}

// ParseRelTime implements the search.FilterRegistry ParseRelTime seam for the
// timeago/reltime/fuzzytime filters. It returns a canonical RFC3339 string.
//
// Flow (verified against Jackett DateTimeUtil.FromTimeAgo + FromUnknown):
//   - "now"/"just now" -> the reference clock.
//   - unix timestamp (all digits): always seconds (matches Jackett FromUnknown).
//   - ISO 8601 / RFC1123Z absolute strings.
//   - "yesterday"/"today"/"tomorrow [, ] [at ] [HH:mm | h:mm am/pm]" -> clock
//     date +/- a day, at the given time (midnight when absent).
//   - "[weekday] at [time]" -> the most recent occurrence of that weekday
//     (today included) at the given time.
//   - "N unit(s) ago" (sec/min/hour|hr/day/week|wk/month/year) -> offset from now.
//   - missing-year dates ("05-14 22:10", "2 Jan 15:30") -> the clock's year,
//     rolled back one year when the result would be in the future.
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
	if t, matched, err := parseNamedDay(lower, now); matched {
		return formatRel(t, value, err)
	}
	if t, matched, err := parseWeekdayAt(lower, now); matched {
		return formatRel(t, value, err)
	}
	if t, ok := parseTimeAgo(lower, now); ok {
		return t.Format(canonicalLayout), nil
	}
	if t, matched, err := parseMissingYear(lower, now); matched {
		return formatRel(t, value, err)
	}

	return "", fmt.Errorf("%w: relative value %q", ErrUnparseable, value)
}

// formatRel finalizes a committed relative branch: once a branch's pattern
// matched we never fall through — an unresolvable remainder is an error, as
// Jackett's DateTime.Parse throws out of FromUnknown.
func formatRel(t time.Time, value string, err error) (string, error) {
	if err != nil {
		return "", fmt.Errorf("%w: relative value %q: %w", ErrUnparseable, value, err)
	}
	return t.Format(canonicalLayout), nil
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
	// Designator case-folded so "3:30 pm" matches Go's uppercase-only PM token,
	// as .NET's case-insensitive parse would.
	hv := normalizeAMPM(v)
	for _, layout := range humanLayouts {
		if t, err := time.Parse(layout, hv); err == nil {
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

// namedDayPatterns mirror Jackett's _TodayRegexp/_YesterdayRegexp/_TomorrowRegexp
// (`(?i)\btoday(?:[\s,]+(?:at){0,1}\s*|[\s,]*|$)`): the match consumes the day
// word AND its trailing separator — whitespace/commas plus an optional "at" — so
// the remainder is a bare time-of-day ("Today, at 14:22" leaves "14:22").
// Jackett's `|$` alternative is unreachable (`[\s,]*` already matches empty) and
// is omitted. Patterns are lowercase because ParseRelTime lowers the input.
var namedDayPatterns = []struct {
	re   *regexp.Regexp
	days int
}{
	{regexp.MustCompile(`\btoday(?:[\s,]+(?:at)?\s*|[\s,]*)`), 0},
	{regexp.MustCompile(`\byesterday(?:[\s,]+(?:at)?\s*|[\s,]*)`), -1},
	{regexp.MustCompile(`\btomorrow(?:[\s,]+(?:at)?\s*|[\s,]*)`), 1},
}

// parseNamedDay handles "today"/"yesterday"/"tomorrow" with an optional trailing
// time-of-day, anchored to the reference clock's date. Once a day word matches
// we commit, as Jackett does: a non-empty remainder that is not a parseable time
// is an error (Jackett's DateTime.Parse throws out of FromUnknown), never a
// silent midnight.
func parseNamedDay(lower string, now time.Time) (time.Time, bool, error) {
	for _, nd := range namedDayPatterns {
		m := nd.re.FindString(lower)
		if m == "" {
			continue
		}
		dur, err := parseClockTime(strings.TrimSpace(strings.Replace(lower, m, "", 1)))
		if err != nil {
			return time.Time{}, true, err
		}
		day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
		return day.AddDate(0, 0, nd.days).Add(dur), true, nil
	}
	return time.Time{}, false, nil
}

// clockLayouts are the time-of-day forms Jackett's remainder parse
// (DateTime.Parse(time).TimeOfDay) accepts on realistic feed input: 24h with
// optional seconds, plus 12h am/pm variants.
var clockLayouts = []string{"15:04:05", "15:04", "3:04:05 PM", "3:04 PM", "3:04PM", "3 PM", "3PM"}

// parseClockTime parses rest as a duration since midnight. Empty means the day
// alone (Jackett's ParseTimeSpan returns TimeSpan.Zero); a non-empty rest that
// matches no layout is an error, mirroring DateTime.Parse throwing.
func parseClockTime(rest string) (time.Duration, error) {
	if rest == "" {
		return 0, nil
	}
	rest = normalizeAMPM(rest)
	for _, layout := range clockLayouts {
		if t, err := time.Parse(layout, rest); err == nil {
			return time.Duration(t.Hour())*time.Hour +
				time.Duration(t.Minute())*time.Minute +
				time.Duration(t.Second())*time.Second, nil
		}
	}
	return 0, fmt.Errorf("unparseable time-of-day %q after named day", rest)
}

// weekdayAtRegexp mirrors Jackett's _DaysOfWeekRegexp
// (`(?i)\b(monday|...|sunday)\s+at\s+`): English weekday names only, with a
// mandatory " at " separator. Lowercase because ParseRelTime lowers the input.
var weekdayAtRegexp = regexp.MustCompile(`\b(monday|tuesday|wednesday|thursday|friday|saturday|sunday)\s+at\s+`)

var weekdaysByName = map[string]time.Weekday{
	"monday":    time.Monday,
	"tuesday":   time.Tuesday,
	"wednesday": time.Wednesday,
	"thursday":  time.Thursday,
	"friday":    time.Friday,
	"saturday":  time.Saturday,
	"sunday":    time.Sunday,
}

// parseWeekdayAt handles "[weekday] at [time]" ("Friday at 22:14"): the most
// recent occurrence of that weekday at or before the clock's date, at the given
// time. TODAY counts as a match — Jackett's walk-back loop
// (`while (dt.DayOfWeek != dow) dt = dt.AddDays(-1)`) starts at today's date and
// never skips back a week, even when the time of day is ahead of the clock.
func parseWeekdayAt(lower string, now time.Time) (time.Time, bool, error) {
	m := weekdayAtRegexp.FindStringSubmatch(lower)
	if m == nil {
		return time.Time{}, false, nil
	}
	dur, err := parseClockTime(strings.TrimSpace(strings.Replace(lower, m[0], "", 1)))
	if err != nil {
		return time.Time{}, true, err
	}
	day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	for day.Weekday() != weekdaysByName[m[1]] {
		day = day.AddDate(0, 0, -1)
	}
	return day.Add(dur), true, nil
}

// Missing-year forms, mirroring Jackett's _MissingYearRegexp
// (`^(\d{1,2}-\d{1,2})(\s|$)`, month-day: "05-14 22:10") and _MissingYearRegexp2
// (`^(\d{1,2}\s+\w{3})\s+(\d{1,2}:\d{1,2}.*)$`, "2 Jan 15:30"). Both resolve
// with the clock's year via the FromFuzzyPastTime rollback. The trailing time
// goes through parseClockTime rather than Jackett's fuzzy parser, so a
// remainder beyond the clock layouts errors instead of fuzzy-matching.
var (
	missingYearNumericRegexp = regexp.MustCompile(`^(\d{1,2})-(\d{1,2})(?:\s+(.*))?$`)
	missingYearMonthRegexp   = regexp.MustCompile(`^(\d{1,2}\s+[a-z]{3})\s+(\d{1,2}:\d{1,2}.*)$`)
)

// parseMissingYear routes the two missing-year forms; the numeric regex is
// tried first, in Jackett's branch order.
func parseMissingYear(lower string, now time.Time) (time.Time, bool, error) {
	if m := missingYearNumericRegexp.FindStringSubmatch(lower); m != nil {
		t, err := missingYearNumericDate(m[1], m[2], m[3], now)
		return t, true, err
	}
	if m := missingYearMonthRegexp.FindStringSubmatch(lower); m != nil {
		t, err := missingYearMonthDate(m[1], m[2], now)
		return t, true, err
	}
	return time.Time{}, false, nil
}

// missingYearNumericDate resolves "MM-dd [time]" with the clock's year. The
// order is month-day because Jackett prepends the year ("2024-" + "05-14") and
// fuzzy-parses the ISO-shaped result. A pair that is not a real month-day (a
// dd-MM feed like "25-12") errors, as Jackett's fuzzy parse would throw.
func missingYearNumericDate(monthStr, dayStr, rest string, now time.Time) (time.Time, error) {
	month, _ := strconv.Atoi(monthStr)
	day, _ := strconv.Atoi(dayStr)
	dur, err := parseClockTime(strings.TrimSpace(rest))
	if err != nil {
		return time.Time{}, err
	}
	dt := time.Date(now.Year(), time.Month(month), day, 0, 0, 0, 0, now.Location())
	if int(dt.Month()) != month || dt.Day() != day {
		return time.Time{}, fmt.Errorf("no such month-day %s-%s", monthStr, dayStr)
	}
	return rollbackIfFuture(dt.Add(dur), now), nil
}

// missingYearMonthDate resolves "d MMM HH:mm" ("2 Jan 15:30") with the clock's
// year. Go's name matching is ASCII case-insensitive, so the lowered "jan"
// parses against the "Jan" token.
func missingYearMonthDate(datePart, timePart string, now time.Time) (time.Time, error) {
	md, err := time.Parse("2 Jan", datePart)
	if err != nil {
		return time.Time{}, fmt.Errorf("missing-year month-day %q: %w", datePart, err)
	}
	dur, err := parseClockTime(strings.TrimSpace(timePart))
	if err != nil {
		return time.Time{}, err
	}
	dt := time.Date(now.Year(), md.Month(), md.Day(), 0, 0, 0, 0, now.Location())
	return rollbackIfFuture(dt.Add(dur), now), nil
}

// rollbackIfFuture applies Jackett's FromFuzzyPastTime rule: a missing-year
// date that lands after the clock belongs to last year.
func rollbackIfFuture(t, now time.Time) time.Time {
	if t.After(now) {
		return t.AddDate(-1, 0, 0)
	}
	return t
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
