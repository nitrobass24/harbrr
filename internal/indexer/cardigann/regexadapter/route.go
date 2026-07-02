package regexadapter

import "strings"

// hasDotNetConstructs reports whether pattern uses a regex construct that RE2
// cannot express and .NET (regexp2) can, forcing the regexp2 route. The set,
// verified against the .NET Regex grammar (Microsoft "Regular Expression
// Language — Quick Reference") and regexp2's supported syntax:
//
//   - Lookahead:        (?=  positive,  (?!  negative
//   - Lookbehind:       (?<= positive,  (?<! negative
//   - Named group:      (?<name>  or (?'name'  — a "(?<" NOT followed by = or !
//   - Atomic group:     (?>
//   - Conditional:      (?(  (by-name or by-number alternation)
//   - Backreference:    \1..\9 (numbered) and \k<name> / \k'name' (named)
//
// RE2 supports the Go named-group form "(?P<name>)" natively, so that spelling
// is NOT a trigger; only the .NET spellings above are. Atomic/conditional do
// not occur in the current corpus but are included for completeness/correctness.
func hasDotNetConstructs(pattern string) bool {
	return hasLookaround(pattern) ||
		hasNamedOrAtomicOrConditional(pattern) ||
		hasBackreference(pattern)
}

// hasLookaround detects lookahead "(?=" / "(?!" and lookbehind "(?<=" / "(?<!".
// A bare "(?<" is disambiguated in hasNamedGroup (it is a named group unless the
// next char is = or !, which makes it a lookbehind — handled here).
func hasLookaround(p string) bool {
	if strings.Contains(p, "(?=") || strings.Contains(p, "(?!") {
		return true
	}
	return strings.Contains(p, "(?<=") || strings.Contains(p, "(?<!")
}

// hasNamedOrAtomicOrConditional detects the remaining "(?"-prefixed .NET groups:
// named "(?<name>" / "(?'name'", atomic "(?>", and conditional "(?(".
func hasNamedOrAtomicOrConditional(p string) bool {
	for i := 0; i+2 < len(p); i++ {
		if p[i] != '(' || p[i+1] != '?' {
			continue
		}
		switch p[i+2] {
		case '>', '(':
			// Atomic group "(?>" or conditional "(?(".
			return true
		case '<':
			// "(?<" is a named group unless it is lookbehind "(?<=" / "(?<!",
			// which hasLookaround already covers. A named group's next char is a
			// name char (letter/underscore), never = or !.
			if i+3 < len(p) && p[i+3] != '=' && p[i+3] != '!' {
				return true
			}
		case '\'':
			// .NET alternate named-group spelling "(?'name'".
			return true
		}
	}
	return false
}

// hasBackreference detects numbered backreferences "\1".."\9" and named
// backreferences "\k<name>" / "\k'name'". A literal class escape (\d, \w, ...)
// or an octal/escape that is not 1..9 is NOT a backreference. We respect escape
// pairing so "\\1" (escaped backslash then literal 1) is not misread.
func hasBackreference(p string) bool {
	for i := 0; i < len(p); i++ {
		if p[i] != '\\' || i+1 >= len(p) {
			continue
		}
		next := p[i+1]
		if next >= '1' && next <= '9' {
			return true
		}
		if next == 'k' && i+2 < len(p) && (p[i+2] == '<' || p[i+2] == '\'') {
			return true
		}
		// Consume the escaped char so "\\1" (escaped backslash) is not treated
		// as a backslash followed by "1".
		i++
	}
	return false
}
