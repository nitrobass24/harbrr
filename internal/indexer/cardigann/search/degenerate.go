package search

import (
	"strings"
	"unicode"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/loader"
)

// yearLen is the token width of a bare four-digit year — the one token a query can
// be reduced to that carries no title signal at all (a Radarr movie query is
// "<title> <year>", so a definition that strips the title leaves exactly this).
const yearLen = 4

// DegenerateQuery reports whether the definition's OWN search.keywordsfilters
// reduced this query's term to something that cannot answer the question asked, so
// the request would cost the tracker a full search and return a set that has to be
// discarded downstream (autobrr/harbrr#394).
//
// Many definitions strip `[^a-zA-Z0-9 ]+` in keywordsfilters, so a CJK, Cyrillic or
// Thai query reduces to whatever Latin survived — frequently just the year. The test
// is deliberately RELATIVE: the raw term must carry signal and the filtered residual
// must not. That is what makes it definition-agnostic and false-positive-free:
//   - an RSS/browse poll (no keywords at all) is degenerate on BOTH sides, so it is
//     never gated — gating it would blank the feed;
//   - a genuine year-only search ("1917") is degenerate on both sides too, so the
//     operator's own query is still sent;
//   - a definition with no keywordsfilters never changes the term, so nothing is
//     ever gated on it;
//   - an id search is never gated: the imdbid/tmdbid/… drives the request, and
//     Jackett's own andmatch skips id searches for the same reason.
//
// It is a PURE predicate — no request, no session, no state — so the caller can run
// it before spending anything (see the registry adapter, which gates before the
// search cache and the request budget).
func DegenerateQuery(def *loader.Definition, query Query, deps Deps) bool {
	if query.isIDSearch() || degenerateTerm(query.keywords()) {
		return false
	}
	filtered, err := applyKeywordsFilters(def, query, deps)
	if err != nil {
		// A filter that cannot render is a search failure, not a gate decision: let the
		// normal path raise it so the operator sees the real error.
		return false
	}
	return degenerateTerm(filtered.templateKeywords())
}

// degenerateTerm reports whether term carries no usable search signal: nothing but
// separators/whitespace, or nothing but four-digit year tokens. Tokens are split on
// every non-alphanumeric rune, and "alphanumeric" is Unicode-wide (unicode.IsLetter),
// so a CJK/Cyrillic/Thai term counts as signal — the whole point is to notice when
// the definition's filters have thrown that signal away.
func degenerateTerm(term string) bool {
	tokens := strings.FieldsFunc(term, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	for _, tok := range tokens {
		if !yearToken(tok) {
			return false
		}
	}
	return true
}

// yearToken reports whether tok is a bare four-digit number.
func yearToken(tok string) bool {
	if len(tok) != yearLen {
		return false
	}
	for _, r := range tok {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
