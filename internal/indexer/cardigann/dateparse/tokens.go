package dateparse

import (
	"fmt"
	"strings"
)

// netToken pairs a .NET custom date/time format token with its Go reference-time
// equivalent. The Cardigann corpus passes these .NET-style layout strings (e.g.
// "yyyy-MM-dd HH:mm:ss zzz") as the dateparse/timeparse filter arg. Jackett's
// ParseDateTimeGoLang feeds them straight to DateTime.ParseExact with
// InvariantCulture; we instead translate them to a Go layout for time.Parse.
//
// Order matters: tokens are matched greedily longest-first so "yyyy" wins over
// "yy" and "MMMM" over "MMM"/"MM"/"M".
type netToken struct {
	net   string
	goRef string
}

// netTokens is the full translation table, pre-sorted longest-first within each
// token family so the greedy scanner never matches a shorter prefix early.
//
// Token semantics verified against .NET CultureInfo custom format specifiers and
// Go's reference time Mon Jan 2 15:04:05 MST 2006 (-07:00):
//
//	yyyy MMMM dddd  -> 4+ char year / full month / full weekday name
//	yy   MMM  ddd   -> 2-digit year / abbreviated month / weekday name
//	MM   dd         -> zero-padded month / day
//	M    d          -> non-padded month / day
//	HH   H          -> 24h zero-padded / non-padded
//	hh   h          -> 12h zero-padded / non-padded
//	mm   m          -> minute padded / non-padded
//	ss   s          -> second padded / non-padded
//	tt              -> AM/PM designator (Go's "PM" matches the full designator)
//	zzz  zz         -> signed UTC offset +05:30 / +05
//	K               -> round-trip kind: Z or signed offset
//	fffffff..f      -> fractional seconds (trailing-zero-significant)
//	FFFFFFF..F      -> fractional seconds (trailing-zero-insignificant)
//
// NOTE: .NET single `z` (sign + variable-width hours, e.g. "+5"/"-7") has NO Go
// reference-time equivalent — Go's "-7" element only matches the literal "-7",
// never a real "+02" value. Rather than emit a silently-broken mapping, single
// `z` is intentionally absent here, so TranslateLayout errors loudly on it. The
// corpus uses only `zzz` (all 460 offset occurrences), so this is latent today.
//
// Single `t` (the first letter of the AM/PM designator, "A"/"P") is likewise
// absent: Go's "PM" element matches only the full designator, so mapping `t`->"PM"
// silently mistranslated. Bare `t` therefore errors loudly while `tt` stays
// supported; the corpus uses only `tt`, so this too is latent today.
var netTokens = []netToken{
	{"yyyy", "2006"},
	{"yyy", "2006"},
	{"yy", "06"},
	{"y", "06"},
	{"MMMM", "January"},
	{"MMM", "Jan"},
	{"MM", "01"},
	{"M", "1"},
	{"dddd", "Monday"},
	{"ddd", "Mon"},
	{"dd", "02"},
	{"d", "2"},
	{"HH", "15"},
	{"H", "15"},
	{"hh", "03"},
	{"h", "3"},
	{"mm", "04"},
	{"m", "4"},
	{"ss", "05"},
	{"s", "5"},
	{"tt", "PM"},
	{"fffffff", ".0000000"},
	{"ffffff", ".000000"},
	{"fffff", ".00000"},
	{"ffff", ".0000"},
	{"fff", ".000"},
	{"ff", ".00"},
	{"f", ".0"},
	{"FFFFFFF", ".9999999"},
	{"FFFFFF", ".999999"},
	{"FFFFF", ".99999"},
	{"FFFF", ".9999"},
	{"FFF", ".999"},
	{"FF", ".99"},
	{"F", ".9"},
	{"zzz", "-07:00"},
	{"zz", "-07"},
	{"K", "Z07:00"},
}

// formatLetters is the set of single characters that begin a .NET format token.
// A run starting with any other byte is a literal (separators, "at", etc.).
const formatLetters = "yMdHhmstfFzK"

// unsupportedLetters are .NET custom format specifiers with no Go reference-time
// equivalent (g = era, K is handled, Q is non-standard). Encountering one yields
// a loud translation error rather than a silent mistranslation/literal, so a def
// using them surfaces in the census instead of producing wrong dates.
const unsupportedLetters = "gQ"

// TranslateLayout converts a .NET custom date/time format string into the
// equivalent Go reference-time layout. It tokenizes greedily, longest-first, and
// passes any non-token run through verbatim — including the corpus's
// no-separator quirks such as "yyyy-MM-ddHH:mm:ss zzz" where date and time
// tokens abut with no delimiter. An unrecognized format letter yields a
// descriptive error rather than a silent mistranslation.
func TranslateLayout(netLayout string) (string, error) {
	var b strings.Builder
	b.Grow(len(netLayout) + 8)

	for i := 0; i < len(netLayout); {
		c := netLayout[i]
		if strings.ContainsRune(unsupportedLetters, rune(c)) {
			return "", fmt.Errorf("translate layout %q: unsupported .NET format specifier %q", netLayout, string(c))
		}
		if !strings.ContainsRune(formatLetters, rune(c)) {
			b.WriteByte(c)
			i++
			continue
		}
		tok, ok := matchToken(netLayout[i:])
		if !ok {
			return "", fmt.Errorf("translate layout %q: unrecognized format token at %q", netLayout, netLayout[i:])
		}
		b.WriteString(tok.goRef)
		i += len(tok.net)
	}

	return b.String(), nil
}

// matchToken returns the longest netToken whose .NET form prefixes s.
func matchToken(s string) (netToken, bool) {
	for _, t := range netTokens {
		if strings.HasPrefix(s, t.net) {
			return t, true
		}
	}
	return netToken{}, false
}
