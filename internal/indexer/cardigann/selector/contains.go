package selector

import (
	"errors"
	"regexp"
	"strconv"
	"strings"
)

// rewriteContains rewrites every `:contains(...)` pseudo-class in a selector
// into cascadia's `:matches(...)` regexp pseudo-class with a literal-quoted
// pattern, making the text match case-SENSITIVE.
//
// AngleSharp's (Jackett's) `:contains` is a case-sensitive ordinal substring
// test on the element's TextContent (`el.TextContent.Contains(value)`, no
// culture/case flags). cascadia's built-in `:contains` lowercases both the
// argument (parser.go) and the node text (pseudo_classes.go), so it matches a
// strict superset: under first-match-wins `case:` blocks a superset match on
// an earlier arm yields the wrong value, and `rows.remove` drops rows Jackett
// keeps. cascadia's `:matches` evaluates a regexp against the SAME node text
// (concatenated descendant text, identical to TextContent) without lowering,
// so a literal-quoted pattern reproduces AngleSharp exactly — and because the
// rewrite happens in the selector text, the fixed semantics apply wherever
// `:contains` occurs, including inside `:has(...)`, `:not(...)`, and
// multi-`:contains` compounds.
//
// The argument is parsed with cascadia's own grammar (quoted string or CSS
// identifier, CSS escape sequences). Anything cascadia's grammar would reject
// is left untouched so cascadia.Compile reports it against the original text
// (the census ledger tracks the corpus defs that hit this). An existing
// `:matches(...)` argument is copied verbatim, mirroring cascadia's raw
// parseRegex scan, so regex bodies cannot be misread as structure.
func rewriteContains(sel string) string {
	var out strings.Builder
	out.Grow(len(sel))
	i := 0
	for i < len(sel) {
		switch c := sel[i]; c {
		case '\'', '"':
			end := skipCSSString(sel, i)
			out.WriteString(sel[i:end])
			i = end
		case '\\':
			// Copy the escape pair verbatim so an escaped quote, colon, or
			// paren is never misread as structure.
			if i+1 < len(sel) {
				out.WriteString(sel[i : i+2])
				i += 2
			} else {
				out.WriteByte(c)
				i++
			}
		case ':':
			if repl, next, ok := rewritePseudo(sel, i); ok {
				out.WriteString(repl)
				i = next
				continue
			}
			out.WriteByte(':')
			i++
		default:
			out.WriteByte(c)
			i++
		}
	}
	return out.String()
}

// rewritePseudo handles the pseudo-class starting at the ':' at sel[i]. For a
// well-formed `:contains(x)` it returns the case-sensitive `:matches(...)`
// replacement; an existing `:matches(...)` is returned verbatim so its raw
// regex body cannot be misread as structure. ok=false means the pseudo is not
// one the rewriter transforms (including a malformed :contains argument, left
// for cascadia to reject); the caller copies the ':' and rescans.
func rewritePseudo(sel string, i int) (repl string, next int, ok bool) {
	j := i + 1
	for j < len(sel) && isNameByte(sel[j]) {
		j++
	}
	if j >= len(sel) || sel[j] != '(' {
		return "", 0, false
	}
	switch strings.ToLower(sel[i+1 : j]) {
	case "contains":
		val, end, argOK := parseContainsArg(sel, j+1)
		if !argOK {
			return "", 0, false
		}
		if val == "" {
			// Contains("") is always true in both engines; keep the original
			// form (cascadia rejects an empty :matches pattern at the end of
			// a selector).
			return sel[i:end], end, true
		}
		return ":matches(" + regexLiteral(val) + ")", end, true
	case "matches", "matchesown":
		end := skipRegex(sel, j+1)
		return sel[i:end], end, true
	}
	return "", 0, false
}

// regexBracketEscaper rewrites the pattern bytes cascadia's parseRegex treats
// as structure. parseRegex finds the end of a `:matches(...)` argument by
// counting raw '('/')' and '['/']' bytes (it is escape-unaware), and the
// preceding consumeParenthesis skips leading whitespace, so parens, brackets,
// and whitespace must be hex-escaped rather than appear literally.
// regexp.QuoteMeta has already escaped every paren/bracket as `\(` etc., so
// replacing those two-byte forms removes every raw occurrence.
var regexBracketEscaper = strings.NewReplacer(
	`\(`, `\x28`, `\)`, `\x29`, `\[`, `\x5B`, `\]`, `\x5D`,
	" ", `\x20`, "\t", `\x09`, "\n", `\x0A`, "\r", `\x0D`, "\f", `\x0C`,
)

// regexLiteral returns a regexp pattern that matches val as a literal
// substring and survives cascadia's raw parseRegex scan.
func regexLiteral(val string) string {
	return regexBracketEscaper.Replace(regexp.QuoteMeta(val))
}

// parseContainsArg parses the argument of `:contains(` starting just past the
// open paren, mirroring cascadia's grammar: whitespace/comments, a quoted
// string or identifier, whitespace/comments, ')'. It returns the unescaped
// value and the index just past the closing paren. ok=false means cascadia
// would reject the argument too; the caller leaves the text untouched so the
// compile error surfaces against the original selector.
func parseContainsArg(s string, i int) (val string, end int, ok bool) {
	i = skipSpaceAndComments(s, i)
	if i >= len(s) {
		return "", 0, false
	}
	var err error
	if s[i] == '\'' || s[i] == '"' {
		val, i, err = parseCSSString(s, i)
	} else {
		val, i, err = parseCSSIdentifier(s, i)
	}
	if err != nil {
		return "", 0, false
	}
	i = skipSpaceAndComments(s, i)
	if i >= len(s) || s[i] != ')' {
		return "", 0, false
	}
	return val, i + 1, true
}

// parseCSSString parses a single- or double-quoted CSS string starting at the
// opening quote, mirroring cascadia's parseString (escape sequences, escaped
// line continuations, error on a raw newline). It returns the unescaped value
// and the index just past the closing quote.
func parseCSSString(s string, i int) (string, int, error) {
	quote := s[i]
	i++
	var b strings.Builder
	for i < len(s) {
		switch s[i] {
		case '\\':
			if i+1 < len(s) {
				switch s[i+1] {
				case '\r':
					if i+2 < len(s) && s[i+2] == '\n' {
						i += 3
						continue
					}
					i += 2
					continue
				case '\n', '\f':
					i += 2
					continue
				}
			}
			val, next, err := parseCSSEscape(s, i)
			if err != nil {
				return "", 0, err
			}
			b.WriteString(val)
			i = next
		case quote:
			return b.String(), i + 1, nil
		case '\r', '\n', '\f':
			return "", 0, errMalformedArg
		default:
			b.WriteByte(s[i])
			i++
		}
	}
	return "", 0, errMalformedArg
}

// parseCSSIdentifier parses a CSS identifier starting at i, mirroring
// cascadia's parseIdentifier/parseName (leading hyphens, name characters,
// escape sequences). It returns the unescaped value and the index just past
// the identifier.
func parseCSSIdentifier(s string, i int) (string, int, error) {
	hyphens := 0
	for i < len(s) && s[i] == '-' {
		i++
		hyphens++
	}
	if i >= len(s) || (!isNameStart(s[i]) && s[i] != '\\') {
		return "", 0, errMalformedArg
	}

	var b strings.Builder
	for i < len(s) {
		switch c := s[i]; {
		case isNameChar(c):
			b.WriteByte(c)
			i++
		case c == '\\':
			val, next, err := parseCSSEscape(s, i)
			if err != nil {
				return "", 0, err
			}
			b.WriteString(val)
			i = next
		default:
			return strings.Repeat("-", hyphens) + b.String(), i, nil
		}
	}
	return strings.Repeat("-", hyphens) + b.String(), i, nil
}

// parseCSSEscape parses a backslash escape starting at the backslash,
// mirroring cascadia's parseEscape: a hex escape of up to six digits consumes
// one trailing whitespace character; anything else escapes the single next
// byte. It returns the unescaped value and the index just past the escape.
func parseCSSEscape(s string, i int) (string, int, error) {
	if len(s) < i+2 {
		return "", 0, errMalformedArg
	}
	start := i + 1
	c := s[start]
	switch {
	case c == '\r' || c == '\n' || c == '\f':
		return "", 0, errMalformedArg
	case isHexDigit(c):
		j := start
		for j < start+6 && j < len(s) && isHexDigit(s[j]) {
			j++
		}
		v, _ := strconv.ParseUint(s[start:j], 16, 64)
		if j < len(s) {
			switch s[j] {
			case '\r':
				j++
				if j < len(s) && s[j] == '\n' {
					j++
				}
			case ' ', '\t', '\n', '\f':
				j++
			}
		}
		return string(rune(v)), j, nil
	}
	return s[start : start+1], start + 1, nil
}

// errMalformedArg signals a :contains argument cascadia's grammar rejects; the
// rewriter leaves such text verbatim so cascadia produces the compile error.
var errMalformedArg = errors.New("malformed :contains argument")

// skipSpaceAndComments mirrors cascadia's skipWhitespace: whitespace bytes and
// complete /* ... */ comments.
func skipSpaceAndComments(s string, i int) int {
	for i < len(s) {
		switch s[i] {
		case ' ', '\t', '\r', '\n', '\f':
			i++
		case '/':
			if !strings.HasPrefix(s[i:], "/*") {
				return i
			}
			// cascadia searches for the closing "*/" AFTER the opening "/*"
			// (strings.Index(p.s[i+2:], "*/")), so "/*/" is NOT a complete
			// comment (its "/" tail never closes); mirror that exactly.
			end := strings.Index(s[i+2:], "*/")
			if end < 0 {
				return i
			}
			i += len("/*") + end + len("*/")
		default:
			return i
		}
	}
	return i
}

// skipCSSString returns the index just past a quoted string starting at the
// opening quote, without unescaping (the text is copied verbatim). An
// unterminated string consumes the rest of the selector; cascadia reports it.
func skipCSSString(s string, i int) int {
	quote := s[i]
	i++
	for i < len(s) {
		switch s[i] {
		case '\\':
			i += 2
		case quote:
			return i + 1
		default:
			i++
		}
	}
	return len(s)
}

// skipRegex returns the index of the ')' or ']' that ends a `:matches(...)`
// argument, mirroring cascadia's parseRegex: raw bytes, counting paren/bracket
// balance, no escape awareness.
func skipRegex(s string, i int) int {
	open := 0
	for i < len(s) {
		switch s[i] {
		case '(', '[':
			open++
		case ')', ']':
			open--
			if open < 0 {
				return i
			}
		}
		i++
	}
	return len(s)
}

func isNameByte(c byte) bool {
	return 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || '0' <= c && c <= '9' || c == '-' || c == '_'
}

// isNameStart / isNameChar / isHexDigit mirror cascadia's nameStart /
// nameChar / hexDigit byte classes.
func isNameStart(c byte) bool {
	return 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || c == '_' || c > 127
}

func isNameChar(c byte) bool {
	return isNameStart(c) || c == '-' || '0' <= c && c <= '9'
}

func isHexDigit(c byte) bool {
	return '0' <= c && c <= '9' || 'a' <= c && c <= 'f' || 'A' <= c && c <= 'F'
}
