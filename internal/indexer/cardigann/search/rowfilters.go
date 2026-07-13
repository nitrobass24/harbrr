package search

import (
	"regexp"
	"strings"
)

// Row filters operate on the row SET (RowsBlock.Filters), not on a single field
// value, so they are exposed as reusable helpers rather than chained through
// apply. Their APPLICATION to the parsed row set is wired by the selector stage
// and the end-to-end Definition walk. The registry only needs to KNOW their
// names (see rowFilterKnown) so the corpus completeness test sees zero unknown
// filters.

// andMatchSplit mirrors Jackett's MatchQueryStringAND tokenizer: split on runs
// of non-word characters (.NET Regex "[^\\w]+"). .NET's \w is Unicode-aware —
// it is exactly [\p{L}\p{Mn}\p{Nd}\p{Pc}] (letters, non-spacing marks, decimal
// digits, connector punctuation). RE2's own \w is ASCII-only, so we spell out
// the Unicode class instead; otherwise a multi-word non-Latin query ("война
// мир", "三体 刘慈欣") would collapse to zero tokens and andMatch would keep every
// row (a superset vs Jackett). RE2 supports these Unicode general categories.
var andMatchSplit = regexp.MustCompile(`[^\p{L}\p{Mn}\p{Nd}\p{Pc}]+`)

// andMatchStopwords are the short words Jackett drops from the keyword set
// before requiring an AND-match.
var andMatchStopwords = map[string]struct{}{"and": {}, "the": {}, "an": {}}

// andMatch implements the core andmatch row test: tokenize keywords on
// non-word runs, drop tokens of length ≤1 and the stopwords, and keep the row
// iff its title contains every remaining token (case-insensitively). Jackett
// additionally skips this filter entirely for ID-based searches (imdb/tmdb/…)
// and supports an optional character-limit arg on the keywords — that
// search-context gating is the caller's job (the selector stage and the
// end-to-end walk); this helper covers the title-vs-keywords matching itself.
//
// NOTE: RowFilterBlock.Args is intentionally NOT threaded here. Row-filter
// application (and any optional andmatch arg) lives in the selector stage and
// the end-to-end walk; the signature will gain the arg at that seam if the
// corpus requires it. The completeness gate checks only the filter NAME, not
// its arg shape.
func andMatch(title, keywords string) bool {
	lowerTitle := strings.ToLower(title)
	for _, raw := range andMatchSplit.Split(keywords, -1) {
		// Jackett drops parts with .NET string Length ≤ 1 — UTF-16 code units,
		// counted here exactly: a BMP rune (single CJK/Cyrillic char) is one
		// unit → dropped, just like Jackett, while an astral rune (CJK
		// extensions) is a surrogate pair (Length 2) → kept and required. The
		// count also covers the empty strings the split leaves at boundaries.
		if utf16Len(raw) <= 1 {
			continue
		}
		tok := strings.ToLower(raw)
		if _, stop := andMatchStopwords[tok]; stop {
			continue
		}
		if !strings.Contains(lowerTitle, tok) {
			return false
		}
	}
	return true
}

// utf16Len is .NET string Length: the number of UTF-16 code units, counting
// each astral rune as its surrogate pair (two units).
func utf16Len(s string) int {
	n := 0
	for _, r := range s {
		n++
		if r > 0xFFFF {
			n++
		}
	}
	return n
}

// strDump implements the strdump row filter: Jackett only debug-logs the row
// and keeps it, so this is a passthrough that always retains the row.
func strDump(_ string) bool {
	return true
}

// rowFilterNames is the bounded set of row-filter names from the schema
// vocabulary. They are recognized (so the corpus test passes) but applied by
// items 5/10, not by apply.
var rowFilterNames = map[string]struct{}{
	"andmatch": {},
	"strdump":  {},
}

// rowFilterKnown reports whether name is a recognized row filter.
func rowFilterKnown(name string) bool {
	_, ok := rowFilterNames[name]
	return ok
}
