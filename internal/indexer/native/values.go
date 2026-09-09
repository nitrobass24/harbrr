package native

import (
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/dateparse"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/loader"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/mapper"
)

// CanonicalIMDBID returns "tt%07d" for any recognisable IMDB id form ("tt0133093",
// "0133093", " TT133093 "), or "" when the input carries no POSITIVE numeric id — there
// is no real id 0, so a zero/negative/blank/non-numeric value is "absent", matching the
// engine's normalizer. Every native family's Release.IMDBID and tt-form request param
// goes through here.
func CanonicalIMDBID(raw string) string {
	n := IMDBNumber(raw)
	if n <= 0 {
		return ""
	}
	return fmt.Sprintf("tt%07d", n)
}

// IMDBNumber is the numeric half for families whose API wants a bare number (hdbits'
// JSON body, newznab's imdbid param); 0 when absent/non-positive.
func IMDBNumber(raw string) int64 {
	s := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(raw)), "tt")
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n <= 0 {
		return 0
	}
	return n
}

// PublishDate parses a tracker timestamp — absolute ISO/RFC forms (including the
// no-colon "+0000" offset trackers emit), unix epochs, or a relative "N units ago"/"now"
// — through the engine's dateparse and returns it as RFC3339 in UTC, the form every
// native Release carries. clock is the driver's Base.Clock (relative forms need a
// reference instant). The error wraps dateparse.ErrUnparseable; callers add their
// family prefix and search.ErrParseError where the family surfaces bad dates.
func PublishDate(raw string, clock func() time.Time) (string, error) {
	s, err := dateparse.New(dateparse.WithClock(clock)).ParseRelTime(strings.TrimSpace(raw))
	if err != nil {
		return "", err //nolint:wrapcheck // the caller adds the family prefix; the dateparse sentinel must stay reachable
	}
	// ParseRelTime formats with the RFC3339 layout, but a year outside 0000–9999 — a
	// 13-digit "millisecond" epoch read as seconds, per Jackett parity — is not RFC3339
	// and fails to reparse. That is an unparseable timestamp, classified like any other.
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return "", fmt.Errorf("%w: %q is outside the RFC3339 year range", dateparse.ErrUnparseable, s)
	}
	return t.UTC().Format(time.RFC3339), nil
}

// CheckboxOn reports whether a stored checkbox setting is checked — the same truthy
// set the cardigann engine's checkbox canonicalisation accepts (harbrr stores a checked
// box as Jackett's "True" sentinel; "true"/"1"/"on"/"yes" are accepted case-insensitively
// so whatever the management API persists is read consistently).
func CheckboxOn(v string) bool { return loader.CheckboxOn(v) }

// DailyEpisodeDate parses a daily-show season/episode pair — season a four-digit year,
// episode "MM/dd" — into the date it names, reproducing Prowlarr's
// DateTime.TryParseExact($"{Season} {Episode}", "yyyy MM/dd"). The four-digit-year guard
// keeps Go's lenient year parsing from matching a normal season (the month/day widths are
// already fixed by the layout). The date is returned unformatted because the families
// disagree on the rendering: the base episode string and BTN/FileList/Nebulance want
// "yyyy.MM.dd", while HDBits' and BeyondHD's APIs want ISO "yyyy-MM-dd".
func DailyEpisodeDate(season, episode string) (time.Time, bool) {
	season, episode = strings.TrimSpace(season), strings.TrimSpace(episode)
	if len(season) != 4 {
		return time.Time{}, false
	}
	t, err := time.Parse("2006 01/02", season+" "+episode)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

// FirstStandardCat returns the first STANDARD newznab category id in ids as a
// one-element slice, or nil when ids carries none. The caps mapper resolves one tracker
// category to both its standard newznab id and Jackett's synthesised 1:1 custom category
// (mapper.CustomCategoryOffset and above); Prowlarr emits exactly one category per
// release, so every native family keeps the standard id and drops the synthetic one. A
// family with a fallback category applies it to the nil.
func FirstStandardCat(ids []int) []int {
	for _, id := range ids {
		if id < mapper.CustomCategoryOffset {
			return []int{id}
		}
	}
	return nil
}

// SanitizeSearchTerm reproduces Prowlarr's SearchCriteriaBase.SanitizedSearchTerm:
// collapse any run of Unicode dash punctuation to a single '-', normalise the
// grave/acute/curly single quotes to a plain apostrophe, then keep only letters, digits,
// whitespace and the punctuation a tracker search term tolerates (-._()@/'[]+%); every
// other rune is dropped. The '-' is absent from the whitelist below because the dash
// branch above has already consumed it.
func SanitizeSearchTerm(term string) string {
	var b strings.Builder
	b.Grow(len(term))
	prevDash := false
	for _, r := range term {
		if unicode.Is(unicode.Pd, r) { // any dash punctuation -> a single '-'
			if !prevDash {
				b.WriteByte('-')
				prevDash = true
			}
			continue
		}
		prevDash = false
		switch {
		case r == '`', r == '´', r == '‘', r == '’':
			b.WriteByte('\'')
		case unicode.IsLetter(r), unicode.IsDigit(r), unicode.IsSpace(r), strings.ContainsRune("._()@/'[]+%", r):
			b.WriteRune(r)
		}
	}
	return b.String()
}
