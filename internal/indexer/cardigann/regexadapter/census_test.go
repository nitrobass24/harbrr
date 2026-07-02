package regexadapter

import (
	"sort"
	"testing"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/loader"
)

// TestCorpusCensus is the headline gate: it loads every vendored definition,
// extracts every regex pattern (re_replace/regexp/validate-arg... — actually
// only re_replace and regexp carry regex; see collectPatterns), routes each
// through Compile with the def's language, and asserts it compiles under the
// chosen engine. A pattern that compiles under NEITHER engine fails the test
// (never silent). The routing breakdown is reported via t.Logf.
//
// There is no knownUnsupported baseline today: the assertion below treats any
// uncompilable pattern as a hard failure. If the upstream snapshot ever adds
// one, convert it to an explicit, visible baseline here rather than silencing.
func TestCorpusCensus(t *testing.T) {
	t.Parallel()

	defs, skipped, err := loader.New("").LoadAll()
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	if len(defs) == 0 {
		t.Fatal("no definitions loaded; cannot run census")
	}
	t.Logf("loaded %d definitions (%d skipped at load)", len(defs), len(skipped))

	stats := newCensusStats()
	for _, def := range defs {
		for _, pat := range collectPatterns(def) {
			compileCensusPattern(t, def, pat, stats)
		}
	}
	stats.report(t)
}

// censusStats accumulates the routing breakdown across the corpus.
type censusStats struct {
	total       int
	re2         int
	regexp2     int
	byTrigger   map[string]int // why a pattern routed to regexp2
	uniquePats  map[string]struct{}
	dotNetCount int
}

func newCensusStats() *censusStats {
	return &censusStats{
		byTrigger:  map[string]int{},
		uniquePats: map[string]struct{}{},
	}
}

func compileCensusPattern(t *testing.T, def *loader.Definition, pat string, s *censusStats) {
	t.Helper()
	s.total++
	s.uniquePats[pat] = struct{}{}

	opts := RouteOptions{Language: def.Language}
	re, err := Compile(pat, opts)
	if err != nil {
		// Compiles under neither engine: hard failure, surfaced with the def id
		// and pattern (def-authored, not secret). No silent skip.
		t.Errorf("def %q: pattern %q compiles under neither engine: %v", def.ID, pat, err)
		return
	}

	switch re.Engine() {
	case EngineRE2:
		s.re2++
	case EngineRegexp2:
		s.regexp2++
		s.byTrigger[regexp2Reason(pat, opts)]++
	}
	if hasDotNetConstructs(pat) {
		s.dotNetCount++
	}
}

// regexp2Reason classifies why a pattern routed to regexp2, for the breakdown.
// Precedence matches Compile's: opt-in, then non-Latin language, then .NET
// constructs, else the RE2-compile-failure fallback.
func regexp2Reason(pat string, opts RouteOptions) string {
	switch {
	case opts.OptIn:
		return "opt-in"
	case isNonLatinScript(opts.Language):
		return "non-Latin-language"
	case hasDotNetConstructs(pat):
		return "dotnet-construct"
	case hasDotNetUnicodeBlock(pat):
		return "dotnet-unicode-block"
	default:
		return "re2-compile-failure"
	}
}

func (s *censusStats) report(t *testing.T) {
	t.Helper()
	t.Logf("census: %d regex patterns (%d unique) across the corpus",
		s.total, len(s.uniquePats))
	t.Logf("routing: RE2=%d  regexp2=%d  (.NET-construct patterns=%d)",
		s.re2, s.regexp2, s.dotNetCount)

	reasons := make([]string, 0, len(s.byTrigger))
	for r := range s.byTrigger {
		reasons = append(reasons, r)
	}
	sort.Strings(reasons)
	for _, r := range reasons {
		t.Logf("  regexp2 via %-20s %d", r, s.byTrigger[r])
	}
}

// collectPatterns walks a definition and returns every regex-bearing string:
// the pattern arg of every re_replace and regexp filter, across all field
// selectors, row filters, keywords/preprocessing filters, and download
// selectors. Only these two filters carry a regex; replace/validate/etc. do
// not. Args[0] is the pattern for both.
func collectPatterns(def *loader.Definition) []string {
	var out []string
	add := func(fbs []loader.FilterBlock) {
		out = append(out, patternsFromFilters(fbs)...)
	}

	for _, fe := range def.Search.Fields.Ordered() {
		add(fe.Block.Filters)
	}
	add(def.Search.KeywordsFilters)
	add(def.Search.PreprocessingFilters)
	out = append(out, patternsFromRowFilters(def.Search.Rows.Filters)...)

	if def.Download != nil {
		for _, sel := range def.Download.Selectors {
			add(sel.Filters)
		}
	}
	return out
}

// regexFilterNames are the filter ops whose first arg is a regex pattern.
var regexFilterNames = map[string]struct{}{
	"re_replace": {},
	"regexp":     {},
}

func patternsFromFilters(fbs []loader.FilterBlock) []string {
	var out []string
	for _, fb := range fbs {
		if _, ok := regexFilterNames[fb.Name]; !ok {
			continue
		}
		if len(fb.Args) > 0 {
			out = append(out, fb.Args[0])
		}
	}
	return out
}

func patternsFromRowFilters(fbs []loader.RowFilterBlock) []string {
	var out []string
	for _, fb := range fbs {
		if _, ok := regexFilterNames[fb.Name]; !ok {
			continue
		}
		if len(fb.Args) > 0 {
			out = append(out, fb.Args[0])
		}
	}
	return out
}
