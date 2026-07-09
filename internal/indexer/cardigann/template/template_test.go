package template

import (
	"strconv"
	"strings"
	"testing"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/loader"
)

// knownDefBugs are templates whose upstream Jackett definition is malformed in a
// way Jackett's regex-based engine tolerates silently but a real parser cannot.
// They are excluded from the corpus gate and tracked here so a future upstream
// fix surfaces (the test will then complain the entry is stale).
//
//   - 1337x: the search path has an unbalanced ')' —
//     "...(eq .Config.disablesort .False))..." — an extra paren that Jackett's
//     regex logic-matcher ignores. Real Go parsing reports "unexpected right
//     paren". This is a def bug, absorbed here rather than worked around.
var knownDefBugs = map[string]bool{
	"1337x": true,
}

func TestEvalTruthiness(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		text    string
		mutate  func(*Context)
		want    string
		wantErr bool
	}{
		{
			name: "missing key is falsy",
			text: `{{ if .Config.cookie }}set{{ else }}unset{{ end }}`,
			want: "unset",
		},
		{
			name:   "empty string is falsy",
			text:   `{{ if .Config.cookie }}set{{ else }}unset{{ end }}`,
			mutate: func(c *Context) { c.Config["cookie"] = "" },
			want:   "unset",
		},
		{
			name:   "unchecked checkbox (False sentinel) is falsy",
			text:   `{{ if .Config.freeleech }}fl{{ else }}no{{ end }}`,
			mutate: func(c *Context) { c.Config["freeleech"] = c.False },
			want:   "no",
		},
		{
			name:   "non-empty string is truthy",
			text:   `{{ if .Config.cookie }}set{{ else }}unset{{ end }}`,
			mutate: func(c *Context) { c.Config["cookie"] = "abc=1" },
			want:   "set",
		},
		{
			name:   "checked checkbox (True sentinel) is truthy",
			text:   `{{ if .Config.freeleech }}fl{{ else }}no{{ end }}`,
			mutate: func(c *Context) { c.Config["freeleech"] = c.True },
			want:   "fl",
		},
		{
			// Jackett's string truthiness is !IsNullOrWhiteSpace, so a
			// whitespace-only Config value is falsy there; Eval normalizes it to
			// "" so Go agrees.
			name:   "whitespace-only string is falsy (IsNullOrWhiteSpace parity)",
			text:   `{{ if .Config.cookie }}set{{ else }}unset{{ end }}`,
			mutate: func(c *Context) { c.Config["cookie"] = "  \t " },
			want:   "unset",
		},
		{
			// DELIBERATE divergence, not parity: the whitespace collapse empties
			// IMDBID to "" so Go's eq sees ""=="" (true) -> "empty". Jackett's eq is
			// a RAW string compare (variables[param] as string, no IsNullOrWhiteSpace),
			// so "   " != null (.False) -> onFalse -> "set". This degenerate
			// whitespace-only eq input is unhit by the corpus; pinned as the accepted
			// behavior. See TestEvalWhitespaceCollapseIsDeliberate.
			name:   "whitespace-only query eq .False collapses (deliberate, not Jackett-raw)",
			text:   `{{ if eq .Query.IMDBID .False }}empty{{ else }}set{{ end }}`,
			mutate: func(c *Context) { c.Query["IMDBID"] = "   " },
			want:   "empty",
		},
		{
			name:   "whitespace-only Keywords is falsy",
			text:   `{{ if .Keywords }}has{{ else }}none{{ end }}`,
			mutate: func(c *Context) { c.Keywords = "   " },
			want:   "none",
		},
		{
			name: "eq .Query.IMDBID .False true when absent",
			text: `{{ if eq .Query.IMDBID .False }}empty{{ else }}set{{ end }}`,
			want: "empty",
		},
		{
			name:   "eq .Query.IMDBID .False false when present",
			text:   `{{ if eq .Query.IMDBID .False }}empty{{ else }}set{{ end }}`,
			mutate: func(c *Context) { c.Query["IMDBID"] = "tt0111161" },
			want:   "set",
		},
		{
			name: "range over empty Categories yields nothing",
			text: `{{ range .Categories }}{{ . }},{{ end }}`,
			want: "",
		},
		{
			name:   "range over non-empty Categories",
			text:   `{{ range .Categories }}{{ . }},{{ end }}`,
			mutate: func(c *Context) { c.Categories = []string{"1", "2", "3"} },
			want:   "1,2,3,",
		},
		{
			name:   "or picks first non-empty",
			text:   `{{ or .Query.a .Query.b }}`,
			mutate: func(c *Context) { c.Query["b"] = "second" },
			want:   "second",
		},
		{
			name: "or both empty yields empty",
			text: `{{ or .Query.a .Query.b }}`,
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx := NewContext()
			if tt.mutate != nil {
				tt.mutate(ctx)
			}
			got, err := Eval(tt.text, ctx)
			if (err != nil) != tt.wantErr {
				t.Fatalf("Eval error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr && got != "" {
				t.Fatalf("Eval(%q) returned %q alongside error; want empty output", tt.text, got)
			}
			if got != tt.want {
				t.Fatalf("Eval(%q) = %q, want %q", tt.text, got, tt.want)
			}
		})
	}
}

// TestEvalWhitespaceCollapseIsDeliberate pins the DELIBERATE, non-parity edge of
// the whitespace-only collapse. normalizeWhitespaceValues empties a whitespace-
// only carrier value to "" on ALL read paths so bare {{ if .X }} truthiness matches
// Jackett's !IsNullOrWhiteSpace. That same collapse also changes INTERPOLATION and
// eq/ne comparison, where Jackett keeps the RAW value — a benign, degenerate
// divergence (whitespace-ONLY .Query.Q / .Config.* / .Result.*) unhit by any
// vendored def or offline golden. This test locks that accepted behavior; if a
// faithful raw-interpolation split ever lands, these wants change deliberately.
// See template.go's collapse contract and the parity README divergence entry.
func TestEvalWhitespaceCollapseIsDeliberate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		text    string
		mutate  func(*Context)
		want    string // harbrr's collapsed output
		jackett string // what Jackett's raw handling would emit (documentation only)
	}{
		{
			name:    "Config interpolation of a space value empties (Jackett keeps the space)",
			text:    `a{{ .Config.sep }}b`,
			mutate:  func(c *Context) { c.Config["sep"] = " " },
			want:    "ab",
			jackett: "a b",
		},
		{
			name:    "Query.Q interpolation of whitespace empties (Jackett keeps it)",
			text:    `q={{ .Query.Q }}`,
			mutate:  func(c *Context) { c.Query["Q"] = "  \t " },
			want:    "q=",
			jackett: "q=  \t ",
		},
		{
			name:    "Result interpolation of whitespace empties (Jackett keeps it)",
			text:    `[{{ .Result.title }}]`,
			mutate:  func(c *Context) { c.Result["title"] = "   " },
			want:    "[]",
			jackett: "[   ]",
		},
		{
			name:    "eq compares collapsed empty, not Jackett's raw value",
			text:    `{{ if eq .Query.IMDBID .False }}empty{{ else }}set{{ end }}`,
			mutate:  func(c *Context) { c.Query["IMDBID"] = " " },
			want:    "empty", // Jackett: raw " " != null (.False) -> "set"
			jackett: "set",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx := NewContext()
			if tt.mutate != nil {
				tt.mutate(ctx)
			}
			got, err := Eval(tt.text, ctx)
			if err != nil {
				t.Fatalf("Eval(%q) error: %v", tt.text, err)
			}
			if got != tt.want {
				t.Fatalf("Eval(%q) = %q, want %q (Jackett would emit %q)", tt.text, got, tt.want, tt.jackett)
			}
		})
	}
}

func TestEvalFuncs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		text    string
		mutate  func(*Context)
		want    string
		wantErr bool
	}{
		{
			name:   "join with comma separator",
			text:   `{{ join .Categories "," }}`,
			mutate: func(c *Context) { c.Categories = []string{"100", "101", "102"} },
			want:   "100,101,102",
		},
		{
			name: "join over empty Categories",
			text: `{{ join .Categories "," }}`,
			want: "",
		},
		{
			name:   "re_replace strips non-alphanumerics",
			text:   `{{ re_replace .Query.Keywords "[^a-zA-Z0-9]+" "%" }}`,
			mutate: func(c *Context) { c.Query["Keywords"] = "the matrix (1999)" },
			want:   "the%matrix%1999%",
		},
		{
			name: "re_replace on empty keyword",
			text: `{{ re_replace .Query.Keywords "[^a-zA-Z0-9]+" "%" }}`,
			want: "",
		},
		{
			name:    "re_replace invalid pattern errors",
			text:    `{{ re_replace .Query.Keywords "[" "%" }}`,
			mutate:  func(c *Context) { c.Query["Keywords"] = "x" },
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx := NewContext()
			if tt.mutate != nil {
				tt.mutate(ctx)
			}
			got, err := Eval(tt.text, ctx)
			if (err != nil) != tt.wantErr {
				t.Fatalf("Eval error = %v, wantErr %v", err, tt.wantErr)
			}
			if err != nil {
				if got != "" {
					t.Fatalf("Eval(%q) returned %q alongside error; want empty output", tt.text, got)
				}
				return
			}
			if got != tt.want {
				t.Fatalf("Eval(%q) = %q, want %q", tt.text, got, tt.want)
			}
		})
	}
}

func TestEvalRealShape(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		text   string
		mutate func(*Context)
		want   string
	}{
		{
			// 1337x-style nested conditional path: pick a search mode based on
			// whether a keyword is present.
			name: "1337x-style nested conditional",
			text: `{{ if .Keywords }}/search/{{ .Keywords }}/1/{{ else }}/trending{{ end }}`,
			mutate: func(c *Context) {
				c.Keywords = "ubuntu"
			},
			want: "/search/ubuntu/1/",
		},
		{
			name: "1337x-style nested conditional, no keyword",
			text: `{{ if .Keywords }}/search/{{ .Keywords }}/1/{{ else }}/trending{{ end }}`,
			want: "/trending",
		},
		{
			// ourbits-style: prefer IMDB search when an IMDBID is present,
			// otherwise fall back to keyword search.
			name: "ourbits-style eq .Query.IMDBID .False chain (imdb present)",
			text: `{{ if ne .Query.IMDBID .False }}imdb={{ .Query.IMDBID }}{{ else }}search={{ .Keywords }}{{ end }}`,
			mutate: func(c *Context) {
				c.Query["IMDBID"] = "tt1375666"
				c.Keywords = "inception"
			},
			want: "imdb=tt1375666",
		},
		{
			name: "ourbits-style eq .Query.IMDBID .False chain (no imdb)",
			text: `{{ if ne .Query.IMDBID .False }}imdb={{ .Query.IMDBID }}{{ else }}search={{ .Keywords }}{{ end }}`,
			mutate: func(c *Context) {
				c.Keywords = "inception"
			},
			want: "search=inception",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx := NewContext()
			if tt.mutate != nil {
				tt.mutate(ctx)
			}
			got, err := Eval(tt.text, ctx)
			if err != nil {
				t.Fatalf("Eval(%q) error: %v", tt.text, err)
			}
			if got != tt.want {
				t.Fatalf("Eval(%q) = %q, want %q", tt.text, got, tt.want)
			}
		})
	}
}

// TestEvalBadIdentRewriteScope pins the scope of the bad-identifier rewrite:
// only text inside {{ ... }} action spans is rewritten (Jackett's
// applyGoTemplateText pattern-matches variables inside actions only). Literal
// text — in particular values spliced in by the expandFuncs phase, like a
// dotted release name — must pass through verbatim.
func TestEvalBadIdentRewriteScope(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		text    string
		mutate  func(*Context)
		want    string
		wantErr bool
	}{
		{
			// nyaasi-style: re_replace output (phase 1) becomes literal text next
			// to a surviving {{ if }} action. The dotted keyword must not be
			// rewritten into (index ...) junk.
			name:   "dotted keyword injected by re_replace stays verbatim",
			text:   `{{ if .Keywords }}{{ re_replace .Keywords "\b0(\d{1})\b" "$1" }}{{ else }}{{ end }}`,
			mutate: func(c *Context) { c.Keywords = "Hotel.del.Luna.2019.1080p" },
			want:   "Hotel.del.Luna.2019.1080p",
		},
		{
			// mypornclub-style path with a dash-separated dotted keyword.
			name:   "dotted keyword next to residual actions in a path",
			text:   `{{ if .Keywords }}s/{{ re_replace .Keywords "\s+" "-" }}{{ else }}ts{{ end }}`,
			mutate: func(c *Context) { c.Keywords = "Top.Gear S01" },
			want:   "s/Top.Gear-S01",
		},
		{
			name:   "leading-digit config key inside action is rewritten",
			text:   `code={{ .Config.2facode }}`,
			mutate: func(c *Context) { c.Config["2facode"] = "123456" },
			want:   "code=123456",
		},
		{
			name:   "dashed config key inside action is rewritten",
			text:   `{{ if .Config.cat-id }}cat={{ .Config.cat-id }}{{ else }}all{{ end }}`,
			mutate: func(c *Context) { c.Config["cat-id"] = "7" },
			want:   "cat=7",
		},
		{
			// Both at once: the action-internal ref is rewritten while the
			// phase-1-injected literal on the same line is untouched.
			name: "literal dotted text and action-internal bad ref coexist",
			text: `{{ re_replace .Keywords "\s+" "+" }}&cat={{ .Config.cat-id }}`,
			mutate: func(c *Context) {
				c.Keywords = "Hotel.del.Luna.2019.1080p"
				c.Config["cat-id"] = "7"
			},
			want: "Hotel.del.Luna.2019.1080p&cat=7",
		},
		{
			// Unclosed "{{" stays a parse error surfaced by the stdlib parser;
			// the span scan must not panic or eat the text.
			name:    "unclosed action still errors, does not panic",
			text:    `{{ if .Keywords }}x`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx := NewContext()
			if tt.mutate != nil {
				tt.mutate(ctx)
			}
			got, err := Eval(tt.text, ctx)
			if (err != nil) != tt.wantErr {
				t.Fatalf("Eval(%q) error = %v, wantErr %v", tt.text, err, tt.wantErr)
			}
			if got != tt.want {
				t.Fatalf("Eval(%q) = %q, want %q", tt.text, got, tt.want)
			}
		})
	}
}

// TestEvalReReplaceResultIsData pins Jackett's single-pass semantics for
// re_replace/join: the resolved result is a VALUE, never re-tokenized as
// template source (CardigannIndexer.cs applyGoTemplateText does
// `template.Replace(all, expanded)` and only the later logic/if/range/simple-
// variable regexes run — the simple-variable pass `{{\s*(\..+?)\s*}}` needs a
// leading dot and a closing `}}`, so unbalanced brace junk in a result is
// emitted verbatim, quietly). A user keyword can carry `{{`-shaped junk into a
// re_replace input; the render must SUCCEED with that junk passed through, not
// fail the whole search with a template parse/execute error.
func TestEvalReReplaceResultIsData(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		text   string
		mutate func(*Context)
		want   string
	}{
		{
			// Probe 1: an unbalanced "{{bar" in the re_replace input. Jackett's
			// simple-variable regex can't match it (no dot, no closing }}), so it
			// survives verbatim. Current code re-parses the spliced text and dies
			// with `function "bar" not defined`.
			name:   "unbalanced open-brace junk in re_replace input stays verbatim",
			text:   `{{ re_replace .Keywords "\s+" "+" }}`,
			mutate: func(c *Context) { c.Keywords = "foo {{bar" },
			want:   "foo+{{bar",
		},
		{
			// Probe 2: the finding's `\s+`->`+` case. The keyword "{{ .Config.x }}"
			// collapses to "{{+.Config.x+}}": the "+" right after "{{" defeats
			// Jackett's simple-variable regex `{{\s*(\..+?)\s*}}` (it anchors on a
			// dot immediately after optional space), so Jackett emits it verbatim.
			// Current code re-parses "{{+.Config.x+}}" as an action and dies.
			name:   "config-shaped action in re_replace result stays verbatim",
			text:   `{{ re_replace .Keywords "\s+" "+" }}`,
			mutate: func(c *Context) { c.Keywords = "{{ .Config.x }}" },
			want:   "{{+.Config.x+}}",
		},
		{
			// Leading-digit key shape inside a result — the "bad number syntax"
			// case the finding calls out. Same `+`-after-`{{` shape, inert as data.
			name:   "leading-digit config-shaped action in result stays verbatim",
			text:   `{{ re_replace .Keywords "\s+" "+" }}`,
			mutate: func(c *Context) { c.Keywords = "x {{ .Config.2y }}" },
			want:   "x+{{+.Config.2y+}}",
		},
		{
			// join result carrying brace junk is likewise inert data.
			name:   "brace junk in join result stays verbatim",
			text:   `{{ join .Categories "," }}`,
			mutate: func(c *Context) { c.Categories = []string{"a", "{{b", "c"} },
			want:   "a,{{b,c",
		},
		{
			// A GENUINE def-authored action alongside a junk-bearing re_replace
			// result: the author's action still resolves, the result stays data.
			name: "genuine def action resolves while result stays data",
			text: `{{ re_replace .Keywords "\s+" "+" }}&cat={{ .Config.catid }}`,
			mutate: func(c *Context) {
				c.Keywords = "foo {{bar"
				c.Config["catid"] = "7"
			},
			want: "foo+{{bar&cat=7",
		},
		{
			// re_replace-then-join ORDER is preserved: re_replace runs first, its
			// result feeds nothing here, and join runs after — both spliced as data.
			name: "re_replace then join order preserved",
			text: `{{ re_replace .Keywords "\s+" "-" }}/{{ join .Categories "," }}`,
			mutate: func(c *Context) {
				c.Keywords = "Top Gear"
				c.Categories = []string{"1", "2"}
			},
			want: "Top-Gear/1,2",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx := NewContext()
			if tt.mutate != nil {
				tt.mutate(ctx)
			}
			got, err := Eval(tt.text, ctx)
			if err != nil {
				t.Fatalf("Eval(%q) error: %v", tt.text, err)
			}
			if got != tt.want {
				t.Fatalf("Eval(%q) = %q, want %q", tt.text, got, tt.want)
			}
		})
	}
}

// TestEvalGenuineActionStillResolves guards that a real def-authored
// `{{ .Config.x }}` (leading-digit / dashed keys included) still parses and
// resolves after the sentinel-splice change — the fix must only make
// re_replace/join RESULTS inert, never disturb author-written actions.
func TestEvalGenuineActionStillResolves(t *testing.T) {
	t.Parallel()

	ctx := NewContext()
	ctx.Config["2facode"] = "123456"
	got, err := Eval(`code={{ .Config.2facode }}`, ctx)
	if err != nil {
		t.Fatalf("Eval error: %v", err)
	}
	if got != "code=123456" {
		t.Fatalf("got %q, want %q", got, "code=123456")
	}
}

// TestCorpusParses is the parse/eval gate for this stage: every template string
// in every vendored definition must Eval cleanly (parse + execute) against a
// representative Context. It proves the whole corpus parses and executes under
// this stage's two-phase model — it does NOT assert byte-for-byte output parity
// against Jackett (that is the later corpus job). Eval reproduces Jackett's
// applyGoTemplateText shape (re_replace/join substitution, bad-identifier
// rewriting), so a failure here means the corpus uses a construct this stage
// does not yet handle.
//
// One excluded class (documented, not silent): knownDefBugs — upstream-malformed
// templates Jackett tolerates but no real parser can (see knownDefBugs).
//
// re_replace patterns are NOT excluded: the regexadapter transparently routes
// RE2→regexp2, so a pattern needing .NET (regexp2) semantics compiles and parses
// cleanly here. Only a pattern that compiles under NEITHER engine is a failure,
// and that failure is caught loudly in `failures` below — never silently deferred.
func TestCorpusParses(t *testing.T) {
	t.Parallel()

	defs, skipped, err := loader.New("").LoadAll()
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	if len(defs) == 0 {
		t.Fatalf("LoadAll returned no definitions (skipped=%d)", len(skipped))
	}

	ctx := representativeContext()
	var (
		parsed   int
		failures []string
	)

	for _, def := range defs {
		if knownDefBugs[def.ID] {
			continue
		}
		for _, tmpl := range collectTemplates(def) {
			parsed++
			_, err := Eval(tmpl, ctx)
			if err == nil {
				continue
			}
			failures = append(failures, def.ID+": "+strconv.Quote(tmpl)+": "+err.Error())
		}
	}

	if len(failures) > 0 {
		t.Fatalf("%d template(s) failed:\n%s", len(failures), strings.Join(failures, "\n"))
	}
	t.Logf("evaluated %d template strings across %d definitions", parsed, len(defs))
}

func representativeContext() *Context {
	ctx := NewContext()
	ctx.Keywords = "example"
	ctx.Categories = []string{"100", "101"}
	ctx.Query["Keywords"] = "example"
	ctx.Today = Today{Year: "2025", Month: "06", Day: "11"}
	ctx.Config["sitelink"] = "https://example.org/"
	ctx.DownloadUri = &DownloadURI{
		AbsoluteUri:  "https://example.org/download/info/42?id=42",
		AbsolutePath: "/download/info/42",
		PathAndQuery: "/download/info/42?id=42",
		Query:        map[string]string{"id": "42"},
	}
	return ctx
}

// collectTemplates walks a definition and returns every string that Jackett
// would feed through applyGoTemplateText: search paths + inputs, rows selector,
// every field selector/text/default + filter args, login inputs, and download
// selectors. Only strings containing "{{" are template-bearing, mirroring
// applyGoTemplateText's early-out.
func collectTemplates(def *loader.Definition) []string {
	var out []string
	add := func(s string) {
		if strings.Contains(s, "{{") {
			out = append(out, s)
		}
	}

	collectSearch(add, &def.Search)
	collectLogin(add, def.Login)
	collectDownload(add, def.Download)
	return out
}

func collectSearch(add func(string), s *loader.Search) {
	add(s.Path)
	for _, p := range s.Paths {
		add(p.Path)
		addInputBlock(add, p.Inputs)
		for _, c := range p.Categories {
			add(c.String())
		}
	}
	addInputBlock(add, s.Inputs)
	add(s.Rows.Selector)
	addSelectorBlockText(add, s.Rows.Text)
	for _, f := range s.Rows.Filters {
		addArgs(add, f.Args)
	}
	for _, fe := range s.Fields.Ordered() {
		addSelector(add, fe.Block)
	}
}

func collectLogin(add func(string), l *loader.Login) {
	if l == nil {
		return
	}
	add(l.Path)
	add(l.SubmitPath)
	addInputs(add, l.Inputs)
	for _, si := range l.SelectorInputs {
		addSelector(add, si)
	}
	for _, si := range l.GetSelectorInps {
		addSelector(add, si)
	}
}

func collectDownload(add func(string), d *loader.DownloadBlock) {
	if d == nil {
		return
	}
	for _, sel := range d.Selectors {
		add(sel.Selector)
		for _, f := range sel.Filters {
			addArgs(add, f.Args)
		}
	}
	if d.Before != nil {
		add(d.Before.Path)
		addInputBlock(add, d.Before.Inputs)
	}
}

func addSelector(add func(string), sel loader.SelectorBlock) {
	add(sel.Selector)
	addSelectorBlockText(add, sel.Text)
	if sel.Default != nil {
		add(sel.Default.String())
	}
	for _, c := range sel.Case.Ordered() {
		add(c.Value.String())
	}
	for _, f := range sel.Filters {
		addArgs(add, f.Args)
	}
}

func addSelectorBlockText(add func(string), text *loader.Scalar) {
	if text != nil {
		add(text.String())
	}
}

func addInputs(add func(string), inputs map[string]loader.Scalar) {
	for _, v := range inputs {
		add(v.String())
	}
}

func addInputBlock(add func(string), inputs loader.InputsBlock) {
	for _, in := range inputs.Ordered() {
		add(in.Value.String())
	}
}

func addArgs(add func(string), args loader.FilterArgs) {
	for _, a := range args {
		add(a)
	}
}
