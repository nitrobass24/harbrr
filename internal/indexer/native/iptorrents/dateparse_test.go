package iptorrents

import (
	"errors"
	"testing"
	"time"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
)

// TestParsePublishDate proves the relative "time ago" parser reproduces
// DateTimeUtil.FromTimeAgo against the fixed clock (2026-06-15 12:00:00 UTC): each unit,
// fractional values, multi-unit strings, "now", and the unknown-unit parse error.
func TestParsePublishDate(t *testing.T) {
	t.Parallel()
	now := fixedClock()
	d := testDriver(nil, nil)
	cases := []struct {
		in   string
		want time.Time
	}{
		{"30 seconds ago", now.Add(-30 * time.Second)},
		{"5 minutes ago", now.Add(-5 * time.Minute)},
		{"2.5 hours ago", now.Add(-150 * time.Minute)},
		{"3 days ago", now.AddDate(0, 0, -3)},
		{"1 week ago", now.Add(-7 * 24 * time.Hour)},
		{"2 months ago", now.Add(-60 * 24 * time.Hour)},
		{"1 year ago", now.Add(-365 * 24 * time.Hour)},
		{"1 day and 2 hours ago", now.Add(-26 * time.Hour)},
		{"just now", now},
		{"5 hrs ago", now.Add(-5 * time.Hour)},
		{"2 wks ago", now.Add(-14 * 24 * time.Hour)},
	}
	for _, tc := range cases {
		got, err := d.parsePublishDate(tc.in)
		if err != nil {
			t.Errorf("parsePublishDate(%q): %v", tc.in, err)
			continue
		}
		if !got.Equal(tc.want) {
			t.Errorf("parsePublishDate(%q) = %s, want %s", tc.in, got, tc.want)
		}
	}

	if _, err := d.parsePublishDate("at some point"); !errors.Is(err, search.ErrParseError) {
		t.Errorf("unknown unit err = %v, want search.ErrParseError", err)
	}
	if _, err := d.parsePublishDate(""); !errors.Is(err, search.ErrParseError) {
		t.Errorf("empty err = %v, want search.ErrParseError", err)
	}
}
