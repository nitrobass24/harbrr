package regexadapter

import "strings"

// RE2's shorthand classes are ASCII-only (\w is [0-9A-Za-z_], \d is [0-9], \s is
// [\t\n\f\r ]); .NET's are Unicode-aware. Jackett compiles def patterns with
// .NET semantics, so a Latin-language def applying \w or \W to accented text —
// btarg's keywordsfilter `(\w+)` -> `+$1` turning "Amélie" into "+Amé+lie", or
// the `\W+` -> `%` filters that make "Amélie Poulain" into "Am%lie%Poulain" —
// sends a DIFFERENT request URL than Jackett, and a `\s` against the U+00A0 that
// `&nbsp;` decodes to misses a match .NET makes (autobrr/harbrr#636).
//
// Routing every such pattern to regexp2 would give up RE2's linear-time
// guarantee on a large slice of the corpus. Instead the shorthands are rewritten
// into the explicit Unicode property classes RE2 does support, using .NET's own
// definitions:
//
//	\w  [\p{L}\p{Mn}\p{Nd}\p{Pc}]        \W  its negation
//	\d  \p{Nd}                           \D  \P{Nd}
//	\s  [\f\n\r\t\v\x{85}\p{Z}]          \S  its negation
//
// (\p{Z} is what brings U+00A0 and the other Unicode spaces into \s.) Patterns
// that cannot be rewritten route to regexp2 instead — see rewriteShorthandClasses.
const (
	// wordMembers is .NET's \w as bracket-expression MEMBERS (no enclosing []),
	// so it can be spliced either into a new class or into an existing one.
	wordMembers = `\p{L}\p{Mn}\p{Nd}\p{Pc}`
	// spaceMembers is .NET's \s as bracket-expression members: the five ASCII
	// control spaces, NEL (U+0085), and the Unicode space separators \p{Z}.
	spaceMembers = `\f\n\r\t\v\x{85}\p{Z}`
)

// shorthandExpansion is one shorthand class's RE2 spelling in the two positions
// it can occur. inside is empty when the shorthand cannot be expressed inside a
// bracket expression: RE2 has no way to negate a MULTI-member set within [...]
// (no class intersection/subtraction), so `[\W]` and `[\S]` have no rewrite and
// their patterns route to regexp2. The single-member \D does: \P{Nd}.
type shorthandExpansion struct {
	outside string
	inside  string
}

// shorthandExpansions maps the escape letter following a backslash to its
// rewrite. Letters absent from the map (\n, \t, \p, ...) are left untouched —
// RE2 and .NET already agree on them.
var shorthandExpansions = map[byte]shorthandExpansion{
	'd': {outside: `\p{Nd}`, inside: `\p{Nd}`},
	'D': {outside: `\P{Nd}`, inside: `\P{Nd}`},
	'w': {outside: "[" + wordMembers + "]", inside: wordMembers},
	'W': {outside: "[^" + wordMembers + "]"},
	's': {outside: "[" + spaceMembers + "]", inside: spaceMembers},
	'S': {outside: "[^" + spaceMembers + "]"},
}

// rewriteShorthandClasses returns pattern with every \w \W \d \D \s \S rewritten
// to .NET's Unicode definition (see the package constants above), and reports
// whether the rewrite is complete. ok is false — with the ORIGINAL pattern
// returned — when the pattern contains \W or \S inside a character class, which
// RE2 cannot express; wantRegexp2 asks the same question and routes those to
// regexp2.
//
// The walk is a tokenizer, not a regex over a regex: it pairs escapes (so the
// literal backslash in `\\w` is not read as a shorthand) and tracks whether it
// is inside a bracket expression (so `[\w-]` expands to members rather than to a
// nested class). A pattern with no backslash at all is returned untouched.
//
// Bracket tracking is deliberately simple: the first unescaped '[' opens a class
// and the first unescaped ']' closes it. The spellings where that is wrong —
// `[]]`, and .NET's class-subtraction `[a-z-[aeiou]]` — are rejected by RE2
// anyway, so Compile's existing RE2-compile-failure fallback catches them and
// routes to regexp2.
func rewriteShorthandClasses(pattern string) (string, bool) {
	if !strings.ContainsRune(pattern, '\\') {
		return pattern, true
	}

	var b strings.Builder
	b.Grow(len(pattern) + 16)
	inClass := false

	for i := 0; i < len(pattern); i++ {
		c := pattern[i]
		if c == '\\' && i+1 < len(pattern) {
			repl, ok := expandShorthand(pattern[i+1], inClass)
			if !ok {
				return pattern, false
			}
			b.WriteString(repl)
			i++
			continue
		}
		switch {
		case c == '[' && !inClass:
			inClass = true
		case c == ']' && inClass:
			inClass = false
		}
		b.WriteByte(c)
	}
	return b.String(), true
}

// expandShorthand returns the replacement text for the escape "\"+esc at the
// given position, and whether it is expressible. A non-shorthand escape is
// returned verbatim (backslash included). ok is false only for \W and \S inside
// a character class.
func expandShorthand(esc byte, inClass bool) (string, bool) {
	exp, ok := shorthandExpansions[esc]
	if !ok {
		return `\` + string(esc), true
	}
	if !inClass {
		return exp.outside, true
	}
	if exp.inside == "" {
		return "", false
	}
	return exp.inside, true
}

// canRewriteShorthand reports whether the RE2 route can express this pattern's
// shorthand classes. It is the routing half of rewriteShorthandClasses.
func canRewriteShorthand(pattern string) bool {
	_, ok := rewriteShorthandClasses(pattern)
	return ok
}

// hasWordBoundary reports whether pattern uses \b or \B. RE2's \b is an ASCII
// word boundary with no Unicode form, and inside a character class .NET's \b is
// a BACKSPACE literal RE2 rejects outright — neither is rewritable, so these
// patterns route to regexp2. Escapes are paired, so the literal backslash in
// `\\b` is not misread as a boundary.
func hasWordBoundary(pattern string) bool {
	for i := 0; i < len(pattern); i++ {
		if pattern[i] != '\\' || i+1 >= len(pattern) {
			continue
		}
		if pattern[i+1] == 'b' || pattern[i+1] == 'B' {
			return true
		}
		i++
	}
	return false
}
