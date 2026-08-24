package native

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/dateparse"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/loader"
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
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return "", err //nolint:wrapcheck // unreachable: ParseRelTime emits RFC3339
	}
	return t.UTC().Format(time.RFC3339), nil
}

// CheckboxOn reports whether a stored checkbox setting is checked — the same truthy
// set the cardigann engine's checkbox canonicalisation accepts (harbrr stores a checked
// box as Jackett's "True" sentinel; "true"/"1"/"on"/"yes" are accepted case-insensitively
// so whatever the management API persists is read consistently).
func CheckboxOn(v string) bool { return loader.CheckboxOn(v) }
