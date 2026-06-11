package dateparse

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// canonicalLayout is the canonical RFC3339 output both ParseDate and
// ParseRelTime emit (2006-01-02T15:04:05Z07:00). The engine's normalizer and the
// Torznab serializer consume this single shape; Jackett emits RFC1123Z, but
// seekbrr standardizes on RFC3339 internally and serializes per-protocol later.
const canonicalLayout = time.RFC3339

// ErrUnparseable is the sentinel for a value the parser could not interpret with
// the given layout (or as any relative form). It mirrors Jackett's FormatException
// path; callers (the filter stage) decide whether to surface or swallow it.
var ErrUnparseable = errors.New("dateparse: value not parseable")

// Parser translates and parses Cardigann date filter values. It is built once
// per definition by the engine (item 10) from the def's `language:` field and is
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
//  4. time.Parse with the translated layout.
//  5. Default a missing year to the clock's current year (Jackett's
//     ParseExact-with-InvariantCulture defaults the year to now.Year).
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

	t, err := time.Parse(goLayout, value)
	if err != nil {
		return "", fmt.Errorf("%w: value %q layout %q (go %q)", ErrUnparseable, value, layout, goLayout)
	}

	t = defaultMissingYear(t, goLayout, p.now())

	return t.Format(canonicalLayout), nil
}

// layoutHasNameToken reports whether a translated Go layout contains a
// month-name or weekday-name token (the only tokens needing localization).
func layoutHasNameToken(goLayout string) bool {
	return strings.Contains(goLayout, "Jan") || strings.Contains(goLayout, "Mon")
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
