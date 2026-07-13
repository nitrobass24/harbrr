package regexadapter

import (
	"regexp"
	"strings"
)

// dotNetBlockToScript translates a .NET Unicode *block* name (the `\p{IsXxx}`
// syntax) to the equivalent Go/RE2/regexp2 Unicode *script* (or general
// category) name. .NET names Unicode ranges by block (e.g. IsCyrillic,
// IsCJKUnifiedIdeographs); Go and regexp2 (which borrows Go's unicode tables)
// name them by script (Cyrillic, Han). The two grammars are otherwise
// incompatible — regexp2 rejects the bare .NET block name — so this is an
// engine-absorbed difference, NOT a def edit (AGENTS.md: differences live in the
// engine).
//
// Coverage is the corpus census footprint plus the common script blocks, so a
// future def using one routes correctly instead of failing to compile. Mappings
// are .NET block -> nearest Go script:
//   - IsCJKUnifiedIdeographs (and the *Extension* blocks) -> Han
//   - IsHiragana/IsKatakana -> Hiragana/Katakana
//   - IsHangulSyllables/IsHangulJamo -> Hangul
//   - IsGreekandCoptic -> Greek; IsBasicLatin -> Latin; etc.
var dotNetBlockToScript = map[string]string{
	"IsCyrillic":                       "Cyrillic",
	"IsCyrillicSupplement":             "Cyrillic",
	"IsCJKUnifiedIdeographs":           "Han",
	"IsCJKUnifiedIdeographsExtensionA": "Han",
	"IsCJKSymbolsandPunctuation":       "Han",
	"IsHiragana":                       "Hiragana",
	"IsKatakana":                       "Katakana",
	"IsHangulSyllables":                "Hangul",
	"IsHangulJamo":                     "Hangul",
	"IsHangulCompatibilityJamo":        "Hangul",
	"IsGreek":                          "Greek",
	"IsGreekandCoptic":                 "Greek",
	"IsArabic":                         "Arabic",
	"IsHebrew":                         "Hebrew",
	"IsThai":                           "Thai",
	"IsBasicLatin":                     "Latin",
	"IsLatinExtendedA":                 "Latin",
	"IsLatinExtendedB":                 "Latin",
	"IsLatin1Supplement":               "Latin",
	"IsArmenian":                       "Armenian",
	"IsGeorgian":                       "Georgian",
	"IsDevanagari":                     "Devanagari",
}

// dotNetBlockRe matches a .NET Unicode block reference inside a pattern, both
// positive `\p{IsXxx}` and negated `\P{IsXxx}`. Group 1 is the \p/\P prefix
// (preserving negation), group 2 is the block name.
var dotNetBlockRe = regexp.MustCompile(`\\([pP])\{(Is[A-Za-z0-9]+)\}`)

// hasDotNetUnicodeBlock reports whether pattern contains a .NET `\p{IsXxx}`
// block name (recognized by us). This is a .NET-only construct: it forces the
// regexp2 route AND requires normalizePattern to rewrite the block name to the
// script name regexp2 accepts.
func hasDotNetUnicodeBlock(pattern string) bool {
	for _, m := range dotNetBlockRe.FindAllStringSubmatch(pattern, -1) {
		if _, ok := dotNetBlockToScript[m[2]]; ok {
			return true
		}
	}
	return false
}

// normalizePattern rewrites .NET Unicode block names to their Go script
// equivalents so the routed engine can compile them. Unknown `\p{IsXxx}` names
// are left untouched (they will surface as a loud compile error rather than
// being silently dropped). Patterns without a recognized block are returned
// unchanged.
func normalizePattern(pattern string) string {
	if !strings.Contains(pattern, "{Is") {
		return pattern
	}
	return dotNetBlockRe.ReplaceAllStringFunc(pattern, func(match string) string {
		m := dotNetBlockRe.FindStringSubmatch(match)
		script, ok := dotNetBlockToScript[m[2]]
		if !ok {
			return match
		}
		return `\` + m[1] + `{` + script + `}`
	})
}
