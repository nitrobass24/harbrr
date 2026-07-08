package selector

import (
	"sort"
	"strings"
	"testing"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/loader"
)

// knownIncompatible is the documented baseline of EXACT literal selectors the
// embedded Jackett snapshot uses that cascadia rejects but AngleSharp accepts.
// The one pattern-level divergence (:scope) is classified in
// classifyIncompatible; this map captures the one-off upstream-def quirks
// that don't generalize. The census fails on any NEW uncompilable selector not
// covered here, so regressions surface immediately.
//
// This is the standing AngleSharp-vs-cascadia incompatibility ledger referenced
// in docs/architecture.md (invariant 2) — never silently skip; record and justify.
var knownIncompatible = map[string]string{
	// Upstream def typo: ".nth-child(n)" should be ":nth-child(n)". AngleSharp's
	// tolerant parser still yields a result; cascadia's strict grammar rejects
	// the bare "(...)" after a class. Fix belongs upstream / in dropin, not here.
	"div.torrent-stats div.nth-child(1)": "upstream typo: .nth-child should be :nth-child",
	"div.torrent-stats div.nth-child(2)": "upstream typo: .nth-child should be :nth-child",
	"div.torrent-stats div.nth-child(3)": "upstream typo: .nth-child should be :nth-child",
	"div.torrent-stats div.nth-child(4)": "upstream typo: .nth-child should be :nth-child",

	// Empty :has() argument — an upstream def quirk AngleSharp tolerates (always
	// false) but cascadia rejects as a parse error.
	"table#torrenttable > tbody > tr:has()": "empty :has() argument rejected by cascadia",

	// :has() with a leading sibling-combinator relative argument (":has(~ ...)").
	// AngleSharp accepts the relative form; this cascadia build parses :has only
	// with a compound/descendant argument, not a leading "~" combinator.
	"div#content > div.poststuff:has(~ div.entry a.download), div#content > div.poststuff ~ div.entry:has(a.download)": "cascadia :has rejects a leading sibling-combinator relative argument",

	// Unquoted multi-word :contains argument: cascadia's grammar (mirrored by
	// the engine's case-sensitivity rewrite) ends the identifier at the space
	// after "Only" and never reaches the closing paren. Fix belongs upstream /
	// in dropin. (dasunerwartete-api)
	"free_button:contains(Only Upload)": "unquoted multi-word :contains argument rejected by cascadia",

	// Type selector repeated mid-compound ("...))a:not(...") with no combinator:
	// cascadia stops the compound at the second bare "a" and rejects the
	// leftover bytes. The :contains pseudo itself compiles fine (rewritten to a
	// case-sensitive :matches); the malformed compound is the failure.
	// (puntotorrent)
	`td:nth-child(2) a:not(:contains("HDTV"))a:not(:contains("hdtv"))a:not(:contains("REMUX"))a:not(:contains("Remux"))a:not(:contains("remux"))a:not(:contains("WEB"))a:not(:contains("web"))a:not(:contains("Web"))a:contains("1080"),:contains("2160"):contains("uhd")`: "type selector repeated mid-compound without a combinator rejected by cascadia",
}

// censusResult aggregates one pass over the corpus.
type censusResult struct {
	htmlDefs    int
	jsonDefs    int
	cssCompiled int
	jsonPaths   int
	newFailures map[string]string // selector -> "<def>: <error>"
	knownHit    map[string]bool   // baseline entries actually exercised
}

// TestCorpusCensus is a standing compatibility gate, not a one-time check: it
// loads every vendored definition and, for HTML/XML defs, compiles EVERY CSS
// selector with cascadia (rows.selector, each field selector, case keys,
// remove, count, dateheaders); for JSON defs it parses every field path. A new
// uncompilable selector outside knownIncompatible fails the test.
func TestCorpusCensus(t *testing.T) {
	t.Parallel()

	defs, skipped, err := loader.New("").LoadAll()
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	if len(defs) == 0 {
		t.Fatal("no definitions loaded")
	}

	res := &censusResult{
		newFailures: map[string]string{},
		knownHit:    map[string]bool{},
	}
	for _, def := range defs {
		censusDef(def, res)
	}

	t.Logf("census: %d HTML/XML defs, %d JSON defs, %d CSS selectors compiled, %d JSON paths parsed, %d skipped (load failures)",
		res.htmlDefs, res.jsonDefs, res.cssCompiled, res.jsonPaths, len(skipped))
	t.Logf("knownIncompatible (AngleSharp-only) selectors hit: %d distinct", len(res.knownHit))

	if len(res.newFailures) > 0 {
		t.Fatalf("NEW uncompilable selectors (not in knownIncompatible baseline):\n%s", formatFailures(res.newFailures))
	}
}

// censusDef classifies a def and exercises every selector/path it uses.
func censusDef(def *loader.Definition, res *censusResult) {
	if defIsJSON(def) {
		res.jsonDefs++
		censusJSONDef(def, res)
		return
	}
	res.htmlDefs++
	censusHTMLDef(def, res)
}

// defIsJSON reports whether any search path declares a JSON response. Jackett
// selects the JSON handler per path on Response.Type == "json".
func defIsJSON(def *loader.Definition) bool {
	for i := range def.Search.Paths {
		if r := def.Search.Paths[i].Response; r != nil && r.Type == "json" {
			return true
		}
	}
	return false
}

func censusHTMLDef(def *loader.Definition, res *censusResult) {
	for _, sel := range htmlSelectors(def) {
		checkCSS(def.ID, sel, res)
	}
}

func censusJSONDef(def *loader.Definition, res *censusResult) {
	// JSON paths have no compile step that can fail, so the census validates path
	// SHAPE: every corpus path must tokenize into at least one resolvable
	// key/index token (resolving against an empty doc would assert nothing, since
	// every path trivially "not-found"s there). A path that tokenizes to nothing
	// — e.g. a stray bracket or an all-empty segment string — is a resolver gap we
	// want surfaced. The "$" root selector is the one legitimate empty case.
	for _, p := range jsonPaths(def) {
		res.jsonPaths++
		if rowsPath(p) == "" {
			continue // bare "$"/root, no tokens expected
		}
		if len(tokenizePath(trimDotPrefix(strings.TrimPrefix(p, "$")))) == 0 {
			res.newFailures[p] = def.ID + ": JSON path produced no resolvable tokens"
		}
	}
}

// checkCSS compiles one CSS selector, routing failures to the baseline or the
// new-failure set.
//
// Selectors carrying Go-template syntax ({{ ... }}) are NOT raw CSS: in the real
// pipeline the template seam resolves them to concrete CSS before cascadia ever
// sees them (Jackett applies applyGoTemplateText to Selector.Selector first).
// The census therefore excludes template-bearing selectors from the cascadia
// compile gate — they are a template-stage concern, not a selector-compile one —
// and counts only the literal CSS the corpus feeds to cascadia.
func checkCSS(defID, sel string, res *censusResult) {
	sel = strings.TrimSpace(sel)
	if sel == "" || containsTemplate(sel) {
		return
	}
	res.cssCompiled++
	if _, err := compileCSS(sel); err != nil {
		if reason := classifyIncompatible(sel); reason != "" {
			res.knownHit[sel] = true
			return
		}
		res.newFailures[sel] = defID + ": " + err.Error()
	}
}

// containsTemplate reports whether a selector embeds Go-template syntax, which
// must be evaluated before cascadia compilation.
func containsTemplate(sel string) bool { return strings.Contains(sel, "{{") }

// classifyIncompatible returns a non-empty reason when a literal selector uses a
// construct cascadia rejects but AngleSharp (Jackett) accepts. This is the
// standing AngleSharp-vs-cascadia ledger: every entry is a documented, expected
// divergence, surfaced explicitly rather than silently skipped. A selector not
// classified here is a genuine NEW failure and fails the census.
func classifyIncompatible(sel string) string {
	if r, ok := knownIncompatible[sel]; ok {
		return r
	}
	if strings.Contains(sel, ":scope") {
		// :scope anchors a selector to the query context element. AngleSharp
		// supports it (as does Jackett, alongside its manual :root handling);
		// cascadia does not implement the :scope pseudo-class. The engine scopes
		// queries to the row element directly, so :scope is handled in assembly.
		return "cascadia lacks the :scope pseudo-class"
	}
	return ""
}

// htmlSelectors gathers every CSS selector an HTML/XML def uses.
func htmlSelectors(def *loader.Definition) []string {
	var out []string
	out = appendNonEmpty(out, def.Search.Rows.Selector, def.Search.Rows.Remove)
	out = append(out, caseKeys(def.Search.Rows.Case)...)
	if c := def.Search.Rows.Count; c != nil {
		out = appendSelectorBlock(out, *c)
	}
	if d := def.Search.Rows.DateHeaders; d != nil {
		out = appendSelectorBlock(out, *d)
	}
	for _, fe := range def.Search.Fields.Ordered() {
		out = appendSelectorBlock(out, fe.Block)
	}
	return out
}

// appendSelectorBlock adds the CSS-bearing parts of a SelectorBlock (selector,
// remove, case keys). text/default/attribute are not CSS.
func appendSelectorBlock(out []string, b loader.SelectorBlock) []string {
	out = appendNonEmpty(out, b.Selector, b.Remove)
	return append(out, caseKeys(b.Case)...)
}

// jsonPaths gathers every JSON path a JSON def uses (rows + fields + count).
func jsonPaths(def *loader.Definition) []string {
	var out []string
	out = appendNonEmpty(out, def.Search.Rows.Selector)
	if c := def.Search.Rows.Count; c != nil && c.Selector != "" {
		out = append(out, c.Selector)
	}
	for _, fe := range def.Search.Fields.Ordered() {
		if fe.Block.Selector != "" {
			out = append(out, fe.Block.Selector)
		}
	}
	return out
}

func appendNonEmpty(out []string, vals ...string) []string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			out = append(out, v)
		}
	}
	return out
}

// caseKeys returns a case block's CSS-selector arm keys in definition order,
// dropping the "*" wildcard (which is not a CSS selector to compatibility-check).
func caseKeys(cb loader.CaseBlock) []string {
	out := make([]string, 0, cb.Len())
	for _, c := range cb.Ordered() {
		if c.Key != "*" {
			out = append(out, c.Key)
		}
	}
	return out
}

func formatFailures(m map[string]string) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		b.WriteString("  ")
		b.WriteString(k)
		b.WriteString(" -> ")
		b.WriteString(m[k])
		b.WriteByte('\n')
	}
	return b.String()
}
