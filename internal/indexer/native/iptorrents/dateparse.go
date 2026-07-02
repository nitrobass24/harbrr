package iptorrents

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
)

// timeAgoRegex matches each "<number><unit>" pair in a relative date string, mirroring
// DateTimeUtil's `\s*?([\d\.]+)\s*?([^\d\s\.]+)\s*?`: a (possibly fractional) number
// followed by a non-numeric unit token. Multiple pairs accumulate ("1 day 2 hours").
var timeAgoRegex = regexp.MustCompile(`([0-9.]+)\s*([^0-9\s.]+)`)

// parsePublishDate reproduces DateTimeUtil.FromTimeAgo: a relative "time ago" string is
// subtracted from the current time (the driver clock). "now"/"just now" is the current
// time. Units are matched by substring (sec/min/hour|hr/day/week|wk/month|mo/year), with
// week=7d, month=30d, year=365d and fractional values supported. An unknown unit is a
// parse error (Prowlarr throws InvalidDateException).
func (d *driver) parsePublishDate(s string) (time.Time, error) {
	now := d.clock()
	lower := strings.ToLower(s)
	if strings.Contains(lower, "now") {
		return now, nil
	}

	ago, err := parseTimeAgo(lower)
	if err != nil {
		return time.Time{}, err
	}
	return now.Add(-ago), nil
}

// parseTimeAgo accumulates the duration described by every "<number><unit>" pair in s.
// A string with no recognisable pair, or one carrying an unknown unit, is a parse error.
func parseTimeAgo(s string) (time.Duration, error) {
	s = strings.NewReplacer(",", "", "ago", "", "and", "").Replace(s)
	matches := timeAgoRegex.FindAllStringSubmatch(s, -1)
	if len(matches) == 0 {
		return 0, fmt.Errorf("iptorrents: unparseable relative date %q: %w", s, search.ErrParseError)
	}
	var total time.Duration
	for _, m := range matches {
		val, err := strconv.ParseFloat(m[1], 64)
		if err != nil {
			return 0, fmt.Errorf("iptorrents: bad relative-date number %q: %w", m[1], search.ErrParseError)
		}
		dur, err := unitDuration(m[2], val)
		if err != nil {
			return 0, err
		}
		total += dur
	}
	return total, nil
}

const day = 24 * float64(time.Hour)

// timeAgoUnits maps each relative-date unit (by substring or exact short form) to its
// duration in nanoseconds, ordered so the most specific match wins. It reproduces
// DateTimeUtil's substring tests and 7/30/365-day approximations for week/month/year.
var timeAgoUnits = []struct {
	tokens []string
	per    float64
}{
	{[]string{"sec", "s"}, float64(time.Second)},
	{[]string{"min", "m"}, float64(time.Minute)},
	{[]string{"hour", "hr", "h"}, float64(time.Hour)},
	{[]string{"day", "d"}, day},
	{[]string{"week", "wk", "w"}, 7 * day},
	{[]string{"month", "mo"}, 30 * day},
	{[]string{"year", "y"}, 365 * day},
}

// unitDuration maps one "time ago" unit token and value to a duration. A token matches a
// unit when it contains the unit's long form or equals its single-letter short form.
func unitDuration(unit string, val float64) (time.Duration, error) {
	for _, u := range timeAgoUnits {
		if matchesUnit(unit, u.tokens) {
			return time.Duration(val * u.per), nil
		}
	}
	return 0, fmt.Errorf("iptorrents: unknown relative-date unit %q: %w", unit, search.ErrParseError)
}

// matchesUnit reports whether unit matches any token: a multi-character token by
// substring (e.g. "hours" contains "hour"), a single-character token by exact equality
// (so "m" matches "m" but not every word containing it).
func matchesUnit(unit string, tokens []string) bool {
	for _, tok := range tokens {
		if len(tok) == 1 {
			if unit == tok {
				return true
			}
			continue
		}
		if strings.Contains(unit, tok) {
			return true
		}
	}
	return false
}
