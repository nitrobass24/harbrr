package regexadapter

import (
	"fmt"
	"strings"

	"github.com/dlclark/regexp2"
)

// Replacement-token handling.
//
// Jackett's re_replace is .NET Regex.Replace, whose replacement grammar differs
// from Go's regexp.ReplaceAllString. To get identical output regardless of which
// backend matched, both engines drive their own match loop here and feed each
// match through expandDotNetReplacement, the single source of .NET token truth:
//
//	$$        literal "$"
//	$N $NN    capture group N. .NET reads ALL digits after '$' as a single group
//	          number; there is no greedy fallback to a shorter run. If that one
//	          number names no valid group, the entire "$<digits>" is literal text
//	          (use "${1}1" to force group 1 followed by a literal "1").
//	${name}   capture group by name or number; invalid -> literal token
//	$&        whole match (group 0)
//	$`        input text to the left of the match
//	$'        input text to the right of the match
//	$_        the entire input
//	$+        the LAST-NUMBERED group, unconditionally: "" if that group did not
//	          capture, or the whole match (group 0) when the pattern has no
//	          explicit groups. .NET does NOT pick the highest group that happened
//	          to capture — it always substitutes the last group by number.
//	$ (other) literal "$"
//
// We never reimplement the *matching* engine — only the substitution, which both
// .NET and Go expose enough match metadata to reproduce exactly.

// matchView is the per-match data the expander needs, abstracted over both
// engines so expandDotNetReplacement has one implementation. Portions are
// precomputed by the per-engine builders rather than sliced here, because RE2
// indexes by byte and regexp2 by rune — the builders resolve that, keeping the
// expander engine-agnostic.
type matchView struct {
	input  string   // the whole subject string ($_ token)
	whole  string   // the matched text ($& token, group 0)
	left   string   // text to the left of the match ($` token)
	right  string   // text to the right of the match ($' token)
	groups []string // group[0] = whole match, group[n] = capture n ("" if absent)
	names  []string // names[n] = name of group n ("" if unnamed); index-aligned
	// lastCaptured is the value of the LAST-NUMBERED group, for the .NET "$+"
	// token: "" if that group did not capture, or the whole match (group 0) when
	// the pattern has no explicit groups. .NET substitutes this group
	// unconditionally, not the highest group that happened to capture.
	lastCaptured string
}

// group reports whether n is a real group index for this match and its value.
func (m matchView) group(n int) (string, bool) {
	if n < 0 || n >= len(m.groups) {
		return "", false
	}
	return m.groups[n], true
}

// groupByName resolves a "${...}" reference, which .NET accepts as either a
// group name or an all-digit group number.
func (m matchView) groupByName(name string) (string, bool) {
	for i, n := range m.names {
		if n != "" && n == name {
			return m.groups[i], true
		}
	}
	if isAllDigits(name) {
		return m.group(atoiDigits(name))
	}
	return "", false
}

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

// expandDotNetReplacement renders repl for one match per .NET Regex.Replace.
func expandDotNetReplacement(repl string, m matchView) string {
	if !strings.Contains(repl, "$") {
		return repl
	}
	var b strings.Builder
	b.Grow(len(repl))
	for i := 0; i < len(repl); {
		if repl[i] != '$' {
			b.WriteByte(repl[i])
			i++
			continue
		}
		i = writeReplacementToken(&b, repl, i, m)
	}
	return b.String()
}

// writeReplacementToken handles one '$' at repl[i] and returns the index past
// the token it consumed.
func writeReplacementToken(b *strings.Builder, repl string, i int, m matchView) int {
	if i+1 >= len(repl) {
		b.WriteByte('$') // trailing lone '$' is literal
		return i + 1
	}
	switch c := repl[i+1]; c {
	case '$':
		b.WriteByte('$')
		return i + 2
	case '&', '`', '\'', '_', '+':
		b.WriteString(specialToken(c, m))
		return i + 2
	case '{':
		return writeBracedGroup(b, repl, i, m)
	default:
		if c >= '0' && c <= '9' {
			return writeNumberedGroup(b, repl, i, m)
		}
		b.WriteByte('$') // "$x" for non-token x is a literal '$'
		return i + 1
	}
}

// specialToken renders the .NET single-char replacement specials.
func specialToken(c byte, m matchView) string {
	switch c {
	case '&':
		return m.whole
	case '`':
		return m.left
	case '\'':
		return m.right
	case '_':
		return m.input
	case '+':
		return m.lastCaptured
	default:
		return ""
	}
}

// writeBracedGroup handles "${name}" / "${number}". An unterminated or invalid
// reference is emitted literally, matching .NET.
func writeBracedGroup(b *strings.Builder, repl string, i int, m matchView) int {
	rel := strings.IndexByte(repl[i+2:], '}')
	if rel < 0 {
		b.WriteString("${") // no closing brace: literal
		return i + 2
	}
	name := repl[i+2 : i+2+rel]
	end := i + 2 + rel + 1
	if val, ok := m.groupByName(name); ok {
		b.WriteString(val)
		return end
	}
	b.WriteString(repl[i:end]) // unknown group: literal token
	return end
}

// writeNumberedGroup handles "$N"/"$NN". .NET (and regexp2, the parity source of
// truth) consumes the MAXIMAL digit run as a single group number; if that number
// is not a valid group, the entire "$<digits>" stays literal — it does NOT shrink
// the run. So "$1x" is group 1 + "x" (run is just "1"), but "$12" with fewer than
// 12 groups is the literal "$12", not group 1 + "2". Verified against regexp2's
// native Replace.
func writeNumberedGroup(b *strings.Builder, repl string, i int, m matchView) int {
	j := i + 1
	for j < len(repl) && repl[j] >= '0' && repl[j] <= '9' {
		j++
	}
	if val, ok := m.group(atoiDigits(repl[i+1 : j])); ok {
		b.WriteString(val)
		return j
	}
	// The maximal digit run is not a valid group: '$' and digits are literal.
	b.WriteString(repl[i:j])
	return j
}

// atoiDigits parses an all-digit string to int. Overlong runs (beyond any real
// group count) saturate to a sentinel that group() rejects.
func atoiDigits(s string) int {
	n := 0
	for k := 0; k < len(s); k++ {
		n = n*10 + int(s[k]-'0')
		if n > 1<<20 { // far beyond any real group count; avoid overflow
			return 1 << 20
		}
	}
	return n
}

// regexp2MatchView builds a matchView from a regexp2 *Match. Groups are indexed
// by group NUMBER (so $N resolves correctly): GroupByNumber(n) is .NET's
// Match.Groups[n]. runes is the rune view of input, used for the left/right
// portions in rune space.
func regexp2MatchView(input string, runes []rune, m *regexp2.Match) matchView {
	count := m.GroupCount()
	groups := make([]string, count)
	names := make([]string, count)
	for n := 0; n < count; n++ {
		g := m.GroupByNumber(n)
		if g == nil {
			continue // uncaptured group: leave groups[n] "" (the .NET $N value)
		}
		groups[n] = g.String()
		names[n] = g.Name
	}
	start, end := m.Index, m.Index+m.Length
	return matchView{
		input:        input,
		whole:        string(runes[start:end]),
		left:         string(runes[:start]),
		right:        string(runes[end:]),
		groups:       groups,
		names:        names,
		lastCaptured: groups[count-1], // .NET "$+": last-numbered group, unconditionally
	}
}

// replaceRE2 substitutes every match using RE2's byte-indexed match locations.
// RE2 is linear-time so no timeout is needed; errors are impossible on this path.
func (r *Regexp) replaceRE2(input, repl string) string {
	locs := r.re.FindAllStringSubmatchIndex(input, -1)
	if locs == nil {
		return input
	}
	names := r.re.SubexpNames()
	var b strings.Builder
	prev := 0
	for _, loc := range locs {
		b.WriteString(input[prev:loc[0]])
		b.WriteString(expandDotNetReplacement(repl, re2MatchView(input, loc, names)))
		prev = loc[1]
	}
	b.WriteString(input[prev:])
	return b.String()
}

// re2MatchView builds a matchView from an RE2 byte-offset submatch slice.
func re2MatchView(input string, loc []int, names []string) matchView {
	groups := make([]string, len(loc)/2)
	for g := range groups {
		s, e := loc[2*g], loc[2*g+1]
		if s < 0 || e < 0 {
			continue // uncaptured group: leave groups[g] "" (the .NET $N value)
		}
		groups[g] = input[s:e]
	}
	return matchView{
		input:        input,
		whole:        input[loc[0]:loc[1]],
		left:         input[:loc[0]],
		right:        input[loc[1]:],
		groups:       groups,
		names:        names,
		lastCaptured: groups[len(groups)-1], // .NET "$+": last-numbered group, unconditionally
	}
}

// replaceRegexp2 substitutes every match while CHECKING the per-step error so a
// mid-replace ReDoS timeout surfaces as ErrMatchTimeout instead of being
// swallowed into ("", nil) as regexp2.Replace does (it discards the
// FindNextMatch error after the first match, silently truncating to "").
//
// regexp2 indexes matches by RUNE, so this loop works in rune space and only
// converts to string at the boundaries.
func (r *Regexp) replaceRegexp2(input, repl string) (string, error) {
	m, err := r.re2.FindStringMatch(input)
	if err != nil {
		return "", fmt.Errorf("regexp2 replace: %w", sanitizeRegexp2Err(err))
	}
	if m == nil {
		return input, nil
	}
	runes := []rune(input)
	var b strings.Builder
	prev := 0 // rune offset of the unwritten tail
	for m != nil {
		start, end := m.Index, m.Index+m.Length
		b.WriteString(string(runes[prev:start]))
		b.WriteString(expandDotNetReplacement(repl, regexp2MatchView(input, runes, m)))
		prev = end

		m, err = r.re2.FindNextMatch(m)
		if err != nil {
			return "", fmt.Errorf("regexp2 replace: %w", sanitizeRegexp2Err(err))
		}
	}
	b.WriteString(string(runes[prev:]))
	return b.String(), nil
}
