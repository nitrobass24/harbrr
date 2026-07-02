package regexadapter

import (
	"errors"
	"fmt"
	"strings"
)

// ErrMatchTimeout is returned when a regexp2 match exceeds matchTimeout (the
// ReDoS guard). It is a fixed sentinel deliberately substituted for regexp2's
// native timeout error, which embeds the INPUT text verbatim — that input may
// contain a passkey URL, so it must never propagate into logs. Callers can
// errors.Is against this without ever seeing the matched value.
var ErrMatchTimeout = errors.New("regexp2 match timed out (ReDoS guard)")

// sanitizeRegexp2Err strips secret-bearing detail from a regexp2 error. regexp2
// formats its timeout error as "match timeout after <d> on input `<input>`",
// embedding the raw input. We detect that prefix and replace the whole error
// with ErrMatchTimeout so the matched value never reaches a log. Other regexp2
// errors (compile/runtime) reference structure, not the matched value, so they
// pass through.
func sanitizeRegexp2Err(err error) error {
	if err == nil {
		return nil
	}
	if strings.HasPrefix(err.Error(), "match timeout") {
		return ErrMatchTimeout
	}
	return err
}

// ReplaceAllString replaces every match of the pattern in input with repl,
// mirroring .NET Regex.Replace (Jackett's re_replace).
//
// Both engines run through the same .NET replacement-token expander
// (expandDotNetReplacement) applied per match, so $N / ${name} / $$ / $& / $` /
// $' / $_ / $+ have identical meaning regardless of which backend matched. This
// is why we do NOT defer to regexp2.Replace: that library swallows a mid-replace
// match-timeout (it returns ("", nil) when FindNextMatch errors after the first
// match), which would silently truncate a field to "" under a ReDoS attack
// instead of surfacing ErrMatchTimeout. We drive the match loop ourselves and
// check every step's error.
func (r *Regexp) ReplaceAllString(input, repl string) (string, error) {
	if r.engine == EngineRE2 {
		return r.replaceRE2(input, repl), nil
	}
	return r.replaceRegexp2(input, repl)
}

// FindStringSubmatch returns the leftmost match and its capture groups: index 0
// is the whole match, index n is group n. It returns nil when the pattern does
// not match, matching regexp.FindStringSubmatch's contract on both engines.
func (r *Regexp) FindStringSubmatch(input string) ([]string, error) {
	if r.engine == EngineRE2 {
		return r.re.FindStringSubmatch(input), nil
	}
	m, err := r.re2.FindStringMatch(input)
	if err != nil {
		return nil, fmt.Errorf("regexp2 match: %w", sanitizeRegexp2Err(err))
	}
	if m == nil {
		return nil, nil
	}
	groups := m.Groups()
	out := make([]string, len(groups))
	for i := range groups {
		out[i] = groups[i].String()
	}
	return out, nil
}

// MatchString reports whether the pattern matches anywhere in input.
func (r *Regexp) MatchString(input string) (bool, error) {
	if r.engine == EngineRE2 {
		return r.re.MatchString(input), nil
	}
	ok, err := r.re2.MatchString(input)
	if err != nil {
		return false, fmt.Errorf("regexp2 match: %w", sanitizeRegexp2Err(err))
	}
	return ok, nil
}
