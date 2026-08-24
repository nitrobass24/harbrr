package native

import (
	"errors"
	"testing"
	"time"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/dateparse"
)

func TestCanonicalIMDBID(t *testing.T) {
	t.Parallel()
	cases := []struct{ in, want string }{
		{"tt0133093", "tt0133093"},
		{"0133093", "tt0133093"},
		{"133093", "tt0133093"},
		{" TT133093 ", "tt0133093"},
		{"12345678", "tt12345678"}, // 7 is the minimum width, not a cap
		{"0", ""},
		{"tt0", ""},
		{"tt-5", ""},
		{"-5", ""},
		{"", ""},
		{"tt", ""},
		{"not-an-id", ""},
		{"https://imdb.com/title/tt0133093/", ""},
	}
	for _, c := range cases {
		if got := CanonicalIMDBID(c.in); got != c.want {
			t.Errorf("CanonicalIMDBID(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestIMDBNumber(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   string
		want int64
	}{
		{"tt0133093", 133093},
		{"133093", 133093},
		{" TT123 ", 123},
		{"0", 0},
		{"tt0", 0},
		{"tt-5", 0},
		{"", 0},
		{"abc", 0},
	}
	for _, c := range cases {
		if got := IMDBNumber(c.in); got != c.want {
			t.Errorf("IMDBNumber(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestPublishDate(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	cases := []struct{ in, want string }{
		{"2024-01-15T10:30:00+02:00", "2024-01-15T08:30:00Z"}, // offset -> UTC
		{"2015-04-04T20:30:46+0000", "2015-04-04T20:30:46Z"},  // no-colon offset
		{"2015-04-04T20:30:46-0500", "2015-04-05T01:30:46Z"},
		{"2024-01-15T10:30:00.123456Z", "2024-01-15T10:30:00Z"},
		{"2024-01-15T10:30:00", "2024-01-15T10:30:00Z"}, // bare -> UTC
		{" 2024-01-15 10:30:00 ", "2024-01-15T10:30:00Z"},
		{"1577880000", "2020-01-01T12:00:00Z"},
		{"3 hours ago", "2026-06-15T09:00:00Z"},
		{"2.5 hours ago", "2026-06-15T09:30:00Z"},
		{"1 day and 2 hours ago", "2026-06-14T10:00:00Z"},
		{"5 hrs ago", "2026-06-15T07:00:00Z"},
		{"2 wks ago", "2026-06-01T12:00:00Z"},
		{"just now", "2026-06-15T12:00:00Z"},
		{"now", "2026-06-15T12:00:00Z"},
	}
	for _, c := range cases {
		got, err := PublishDate(c.in, clock)
		if err != nil {
			t.Errorf("PublishDate(%q): %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("PublishDate(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	for _, in := range []string{"", "nope", "at some point", "3 fortnights ago"} {
		if _, err := PublishDate(in, clock); !errors.Is(err, dateparse.ErrUnparseable) {
			t.Errorf("PublishDate(%q) err = %v, want dateparse.ErrUnparseable", in, err)
		}
	}
}

func TestCheckboxOn(t *testing.T) {
	t.Parallel()
	for _, v := range []string{"True", "true", "TRUE", " 1 ", "on", "yes"} {
		if !CheckboxOn(v) {
			t.Errorf("CheckboxOn(%q) = false, want true", v)
		}
	}
	for _, v := range []string{"", "false", "0", "off", "no", "maybe"} {
		if CheckboxOn(v) {
			t.Errorf("CheckboxOn(%q) = true, want false", v)
		}
	}
}
