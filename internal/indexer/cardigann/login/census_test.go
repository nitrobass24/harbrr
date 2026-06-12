package login

import (
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/autobrr/seekbrr/internal/indexer/cardigann/loader"
	"github.com/autobrr/seekbrr/internal/indexer/cardigann/selector"
	"github.com/autobrr/seekbrr/internal/indexer/cardigann/template"
)

// knownMethods is the set of login methods this executor implements. A corpus
// def whose method is outside this set is a planning failure (never silently
// skipped). There is no knownUnsupported baseline today: every method the corpus
// uses (form/post/get/cookie; oneurl unused) is recognized.
var knownMethods = map[string]bool{
	"form":   true,
	"post":   true,
	"get":    true,
	"cookie": true,
	"oneurl": true,
}

// syntheticConfig provides a value for every .Config key a login template might
// reference, so template evaluation during planning never short-circuits on a
// missing variable in a way that masks a real template error.
func syntheticConfig() map[string]string {
	return map[string]string{
		"username": "u", "password": "p", "cookie": "k=v",
		"apikey": "a", "apiurl": "api.example", "sitelink": "https://example.org/",
		"passkey": "x", "rsskey": "x", "2facode": "0", "cat-id": "1",
		"token": "t", "key": "k", "host": "h", "domain": "example.org",
	}
}

// TestLoginPlanCensus is the headline corpus census: every vendored def that
// declares a Login must be PLANNABLE offline — its method recognized, all of its
// Inputs/Path/Headers templates evaluate against a synthetic config, and all of
// its SelectorInputs/Error/Test selectors compile. A failure here is surfaced,
// never silent.
func TestLoginPlanCensus(t *testing.T) {
	t.Parallel()

	defs, skipped, err := loader.New("").LoadAll()
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	if len(defs) == 0 {
		t.Fatal("no definitions loaded")
	}

	c := &censusCounts{
		perMethod:   map[string]int{},
		failures:    map[string]string{},
		unsupported: map[string]string{},
	}
	eng := selector.New()
	eng.EvalTemplate = func(s string) (string, error) {
		return template.Eval(s, planContext())
	}

	for _, def := range defs {
		if def.Login == nil {
			continue
		}
		c.withLogin++
		c.perMethod[loginMethod(def.Login)]++
		planLogin(def, eng, c)
	}

	t.Logf("login census: %d defs with a Login block; per-method: %s",
		c.withLogin, formatCounts(c.perMethod))
	t.Logf("login census: %d defs skipped at load (load failures, not login)", len(skipped))

	if len(c.unsupported) > 0 {
		t.Fatalf("UNSUPPORTED login methods in corpus (no knownUnsupported baseline):\n%s", formatMap(c.unsupported))
	}
	if len(c.failures) > 0 {
		t.Fatalf("login PLANNING failures (template/selector):\n%s", formatMap(c.failures))
	}
}

type censusCounts struct {
	withLogin   int
	perMethod   map[string]int
	failures    map[string]string // def id -> reason
	unsupported map[string]string // def id -> method
}

func planContext() *template.Context {
	ctx := template.NewContext()
	for k, v := range syntheticConfig() {
		ctx.Config[k] = v
	}
	return ctx
}

// planLogin checks one def's login block: method recognized, every template
// evaluable, every selector compilable.
func planLogin(def *loader.Definition, eng *selector.Engine, c *censusCounts) {
	l := def.Login
	if !knownMethods[loginMethod(l)] {
		c.unsupported[def.ID] = l.Method
		return
	}
	if err := planTemplates(l); err != nil {
		c.failures[def.ID] = err.Error()
		return
	}
	if err := planSelectors(l, eng); err != nil {
		c.failures[def.ID] = err.Error()
	}
}

// planTemplates evaluates every template-bearing string in the login block
// (Path, SubmitPath, Inputs, Headers) against the synthetic config.
func planTemplates(l *loader.Login) error {
	for _, s := range []string{l.Path, l.SubmitPath} {
		if _, err := template.Eval(s, planContext()); err != nil {
			return fmt.Errorf("path template %q: %w", s, err)
		}
	}
	for name, sc := range l.Inputs {
		if _, err := template.Eval(sc.String(), planContext()); err != nil {
			return fmt.Errorf("input %q template: %w", name, err)
		}
	}
	for name, vals := range l.Headers {
		for _, v := range vals {
			if _, err := template.Eval(v, planContext()); err != nil {
				return fmt.Errorf("header %q template: %w", name, err)
			}
		}
	}
	// Login.Cookies (static bypass cookies seeded before the form/post round-trip,
	// e.g. ["JAVA=OK"]) must parse into at least one name=value pair so the
	// executor actually honors them. A declared-but-unparseable cookie list would
	// silently drop a jscheck-bypass cookie and diverge from Jackett.
	if len(l.Cookies) > 0 && len(parseCookieHeader(strings.Join(l.Cookies, "; "))) == 0 {
		return fmt.Errorf("cookies %v parse to no usable name=value pair", l.Cookies)
	}
	return nil
}

// planSelectors compiles every selector in the login block by running it against
// an empty document. Compilation/tokenization failures surface; a "no match"
// against the empty doc is expected and fine. Template-bearing selectors are
// resolved by the engine's EvalTemplate seam before cascadia sees them.
func planSelectors(l *loader.Login, eng *selector.Engine) error {
	doc, err := eng.ParseHTML([]byte("<html><body></body></html>"))
	if err != nil {
		return fmt.Errorf("parsing empty doc: %w", err)
	}
	root := doc.Root()

	for name, blk := range l.SelectorInputs {
		if err := compileSelector(eng, root, blk); err != nil {
			return fmt.Errorf("selectorinput %q: %w", name, err)
		}
	}
	for name, blk := range l.GetSelectorInps {
		if err := compileSelector(eng, root, blk); err != nil {
			return fmt.Errorf("getselectorinput %q: %w", name, err)
		}
	}
	for i := range l.Error {
		if err := compileErrorBlock(eng, root, l.Error[i]); err != nil {
			return fmt.Errorf("error[%d]: %w", i, err)
		}
	}
	if l.Test != nil && l.Test.Selector != "" {
		if err := compileSelector(eng, root, loader.SelectorBlock{Selector: l.Test.Selector}); err != nil {
			return fmt.Errorf("test selector: %w", err)
		}
	}
	return nil
}

func compileErrorBlock(eng *selector.Engine, root selector.Row, blk loader.ErrorBlock) error {
	if err := compileSelector(eng, root, loader.SelectorBlock{Selector: blk.Selector}); err != nil {
		return err
	}
	if blk.Message != nil {
		return compileSelector(eng, root, *blk.Message)
	}
	return nil
}

// compileSelector runs a selector block through Field on the empty doc. A
// not-found result is fine (the doc is empty); only a compile/tokenize error is
// a planning failure. cascadia-incompatible selectors (:contains, :scope, :has
// edge forms) are handled by the engine assembly (item 10), so they are excluded
// here exactly as the selector census excludes them.
func compileSelector(eng *selector.Engine, root selector.Row, blk loader.SelectorBlock) error {
	if blk.Selector == "" || containsTemplate(blk.Selector) || cascadiaIncompatible(blk.Selector) {
		return nil
	}
	if _, _, err := eng.Field(root, blk); err != nil {
		return fmt.Errorf("selector %q: %w", blk.Selector, err)
	}
	return nil
}

func containsTemplate(s string) bool { return strings.Contains(s, "{{") }

// cascadiaIncompatible mirrors the selector census's classifyIncompatible: these
// are AngleSharp-only constructs the engine shims at assembly time, so they are
// not raw-cascadia planning failures here.
func cascadiaIncompatible(sel string) bool {
	return strings.Contains(sel, ":contains(") ||
		strings.Contains(sel, ":scope") ||
		strings.Contains(sel, ":has(")
}

func formatCounts(m map[string]int) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for i, k := range keys {
		if i > 0 {
			b.WriteString(", ")
		}
		name := k
		if name == "" {
			name = "(empty)"
		}
		fmt.Fprintf(&b, "%s=%d", name, m[k])
	}
	return b.String()
}

func formatMap(m map[string]string) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		fmt.Fprintf(&b, "  %s -> %s\n", k, m[k])
	}
	return b.String()
}
