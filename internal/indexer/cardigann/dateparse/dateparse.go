package dateparse

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// canonicalLayout is the canonical RFC3339 output both ParseDate and
// ParseRelTime emit (2006-01-02T15:04:05Z07:00). The engine's normalizer and the
// Torznab serializer consume this single shape; Jackett emits RFC1123Z, but
// harbrr standardizes on RFC3339 internally and serializes per-protocol later.
const canonicalLayout = time.RFC3339

// ErrUnparseable is the sentinel for a value the parser could not interpret with
// the given layout (or as any relative form). It mirrors Jackett's FormatException
// path; callers (the filter stage) decide whether to surface or swallow it.
var ErrUnparseable = errors.New("dateparse: value not parseable")

// Parser translates and parses Cardigann date filter values. It is built once
// per definition by the engine from the def's `language:` field and is
// safe for reuse across rows. The clock is injectable so relative-time and
// missing-year resolution are deterministic in tests.
type Parser struct {
	now  func() time.Time
	lang string
}

// Option configures a Parser.
type Option func(*Parser)

// WithLanguage sets the CultureInfo-style language code (e.g. "ru-RU") used to
// recognize localized month/day names. When unset, the parser uses English names
// only, matching Jackett's InvariantCulture dateparse behavior exactly.
func WithLanguage(code string) Option {
	return func(p *Parser) { p.lang = code }
}

// WithClock injects the reference clock used for missing-year defaulting and all
// relative-time math. Defaults to time.Now. Tests pass a fixed clock for
// deterministic assertions.
func WithClock(fn func() time.Time) Option {
	return func(p *Parser) { p.now = fn }
}

// New builds a Parser. Unset options default to time.Now and English names.
func New(opts ...Option) *Parser {
	p := &Parser{now: time.Now}
	for _, opt := range opts {
		opt(p)
	}
	if p.now == nil {
		p.now = time.Now
	}
	return p
}

// ParseDate implements the filter.Registry ParseDate seam: it parses value using
// the .NET-style layout and returns a canonical RFC3339 string.
//
// Flow (verified against Jackett DateTimeUtil.ParseDateTimeGoLang +
// CardigannIndexer applyFilters):
//  1. Normalize internal whitespace (Jackett NormalizeSpace).
//  2. Translate the .NET layout to a Go layout (TranslateLayout).
//  3. If a language is set and the layout carries month/day NAME tokens,
//     substitute localized names -> English so Go's time.Parse can read them.
//  4. If the layout carries an AM/PM designator, uppercase it in the value:
//     .NET ParseExact matches designators case-INsensitively ("3pm" parses),
//     Go's "PM" reference token is uppercase-only.
//  5. time.Parse with the translated layout.
//  6. Default the date components the layout omitted (.NET DateTimeParse
//     terminal-state defaults; see defaultMissingDate).
//
// Unlike the timeago/fuzzytime path, dateparse has NO fuzzy fallback in Jackett:
// a ParseExact failure throws FormatException (logged, value unchanged). We
// surface ErrUnparseable so the filter stage can decide; it must never silently
// pass the raw value through.
func (p *Parser) ParseDate(value, layout string) (string, error) {
	value = normalizeSpace(value)

	goLayout, err := TranslateLayout(layout)
	if err != nil {
		return "", err
	}

	if loc, ok := lookupLocale(p.lang); ok && layoutHasNameToken(goLayout) {
		value = localizeValue(value, loc)
	}

	if strings.Contains(goLayout, "PM") {
		value = normalizeAMPM(value)
	}

	t, err := time.Parse(goLayout, value)
	if err != nil {
		return "", fmt.Errorf("%w: value %q layout %q (go %q)", ErrUnparseable, value, layout, goLayout)
	}

	now := p.now()
	t = defaultMissingDate(t, layout, goLayout, now)
	t = rollbackFutureYearless(t, layout, goLayout, now)

	return t.Format(canonicalLayout), nil
}

// rollbackFutureYearless reproduces the tail of Jackett's
// DateTimeUtil.ParseDateTimeGoLang: a yearless date that lands in the future is
// rolled back one year. Jackett only REACHES that tail when the layout misses
// its early return: `commonStandardFormats = {"y","h","d"}; if
// (commonStandardFormats.Any(layout.ContainsIgnoreCase) && TryParseExact(...))
// return;`. So any layout containing y/h/d (case-insensitive) that parses
// directly never rolls back. Every vendored yearless layout (MM.dd, HH:mm,
// HH:mm zzz, htt MMM. d, MM-dd, MM/dd HH:mm) contains d or h, so this rollback
// is DORMANT for the current corpus and harbrr matches Jackett's non-rollback
// there. It activates only for a letterless, yearless layout (e.g. a bare
// "MMM"), matching Jackett's ParseDateTimeGoLangTest.
func rollbackFutureYearless(t time.Time, netLayout, goLayout string, ref time.Time) time.Time {
	if containsAnyFold(netLayout, "y", "h", "d") {
		return t // Jackett's commonStandardFormats early return: no rollback.
	}
	if strings.Contains(goLayout, "2006") || strings.Contains(goLayout, "06") {
		return t // has a year; Jackett checks !format.Contains("yy").
	}
	if t.After(ref) {
		// A yearless future date rolls back one year. Go's AddDate normalizes
		// Feb 29 -> Mar 1 where .NET AddYears clamps to Feb 28; unreachable in the
		// current corpus (every dateparse layout is .NET-style with y/h/d, gated
		// out above — only a hypothetical Go-style day-bearing layout could carry
		// Feb 29 here).
		return t.AddDate(-1, 0, 0)
	}
	return t
}

// containsAnyFold reports whether s contains any of the (already-lowercase)
// substrings, case-insensitively — matching .NET string.ContainsIgnoreCase
// (InvariantCulture) for the ASCII format letters checked here.
func containsAnyFold(s string, lowerSubs ...string) bool {
	lower := strings.ToLower(s)
	for _, sub := range lowerSubs {
		if strings.Contains(lower, sub) {
			return true
		}
	}
	return false
}

// ampmRe matches an AM/PM designator in any case, standalone or attached to a
// digit ("3pm"), but never inside a word — the boundary guards keep a name like
// "Samstag" intact. RE2-safe (no lookarounds needed: the guards consume one
// non-letter, which is fine since designators never abut letters).
var ampmRe = regexp.MustCompile(`(?i)(^|[^a-zA-Z])([ap]m)([^a-zA-Z]|$)`)

// normalizeAMPM uppercases AM/PM designators so a lowercase or mixed-case value
// ("3:04 pm", "03:04 Pm") parses against Go's uppercase-only PM token, matching
// .NET ParseExact's case-insensitive designator match (Jackett accepts these).
// Applied only when the layout carries a designator token; the surrounding
// boundary characters it consumes are non-letters, unaffected by ToUpper.
func normalizeAMPM(value string) string {
	return ampmRe.ReplaceAllStringFunc(value, strings.ToUpper)
}

// layoutHasNameToken reports whether a translated Go layout contains a
// month-name or weekday-name token (the only tokens needing localization).
func layoutHasNameToken(goLayout string) bool {
	return strings.Contains(goLayout, "Jan") || strings.Contains(goLayout, "Mon")
}

// defaultMissingDate fills the date components the layout omitted, mirroring
// .NET DateTimeParse's terminal-state defaults (DateTimeParse.CheckDefaultDateTime,
// the ParseExact path Jackett rides):
//
//   - NO year/month/day token at all (a time-only layout like torrentqq's
//     "HH:mm") → the full date comes from the reference clock (today).
//   - SOME date tokens present → a missing year defaults to the current year;
//     a missing month/day defaults to 1 — which is exactly Go's zero value
//     (January 1), so only the year needs correcting.
//
// Weekday-name tokens (ddd/dddd) are not date components: they parse a name
// without setting year/month/day, in .NET and Go alike.
func defaultMissingDate(t time.Time, netLayout, goLayout string, ref time.Time) time.Time {
	if !layoutHasDateTokens(netLayout) {
		return time.Date(ref.Year(), ref.Month(), ref.Day(), t.Hour(), t.Minute(),
			t.Second(), t.Nanosecond(), t.Location())
	}
	return defaultMissingYear(t, goLayout, ref)
}

// defaultMissingYear sets the year to ref.Year() when the layout omitted a year
// token, mirroring .NET ParseExact's behavior (an absent year defaults to the
// current year rather than year 0). Go's time.Parse defaults a missing year to
// year 0, so we correct it explicitly.
func defaultMissingYear(t time.Time, goLayout string, ref time.Time) time.Time {
	if strings.Contains(goLayout, "2006") || strings.Contains(goLayout, "06") {
		return t
	}
	return time.Date(ref.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(),
		t.Second(), t.Nanosecond(), t.Location())
}

// normalizeSpace collapses runs of whitespace to a single space and trims ends,
// matching Jackett's ParseUtil.NormalizeSpace applied before parsing.
func normalizeSpace(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
