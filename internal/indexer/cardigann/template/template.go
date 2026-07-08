package template

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"text/template"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/regexadapter"
)

// The corpus does NOT fit a vanilla text/template parser. Jackett's
// applyGoTemplateText (CardigannIndexer.cs) is a "very bad implementation of the
// golang template engine" built from regexes, so it silently accepts constructs
// a real Go parser rejects:
//
//   - re_replace/join patterns embed raw regex text, e.g.
//     {{ re_replace .Keywords "\s+" "-" }}. Go interpreted-string literals reject
//     "\s" ("invalid syntax"); Jackett's regex captures it verbatim.
//   - .Config keys are arbitrary tracker setting names: leading digits
//     (.Config.2facode) and dashes (.Config.cat-id). Go field syntax rejects
//     both ("bad number syntax", "bad character U+002D").
//
// To stay byte-for-byte compatible we mirror Jackett's two-phase shape:
//  1. Resolve join + re_replace by regex substitution against the context (same
//     regexes, same arg order as Jackett) — this is what sidesteps the raw-regex
//     literal problem and pins the arg order exactly.
//  2. Rewrite the remaining variable references with non-Go-identifier keys into
//     index form, then hand the residue (logic funcs eq/ne/and/or/if/range/with
//     + plain interpolation) to the stdlib text/template engine, which gives us
//     real Go evaluation semantics + missingkey=zero truthiness for free.

// reReplaceRe matches {{ re_replace .Var "pattern" "replacement" }} with the
// exact shape Jackett uses (variable, pattern, replacement). The pattern/
// replacement groups are non-greedy so embedded regex text (including
// backslash escapes) is captured raw, never interpreted as a Go string literal.
var reReplaceRe = regexp.MustCompile(`{{\s*re_replace\s+(\.[^\s]+)\s+"(.*?)"\s+"(.*?)"\s*}}`)

// joinRe matches {{ join .Var "sep" }} (variable then separator), Jackett's
// JoinRegex shape.
var joinRe = regexp.MustCompile(`{{\s*join\s+(\.[^\s]+)\s+"(.*?)"\s*}}`)

// badIdentRefRe matches a variable reference whose path contains a segment that
// is not a valid Go identifier (leading digit or a dash), e.g. .Config.2facode
// or .Config.cat-id. Such references must be rewritten to index form before the
// stdlib parser sees them. We match the longest dotted run of ident-ish chars.
var badIdentRefRe = regexp.MustCompile(`\.[A-Za-z_][A-Za-z0-9_]*(?:\.[A-Za-z0-9_-]+)+`)

// Eval renders a single Cardigann template string against ctx, reproducing
// Jackett's applyGoTemplateText behavior closely enough for parity.
//
// Errors wrap the template TEXT (definition-sourced, never secret) and the Go
// error only — never raw ctx values, which may hold passkeys.
func Eval(text string, ctx *Context) (string, error) {
	// Jackett early-outs when there is no "{{": the string passes through
	// untouched. Mirror that so non-template text never hits the parser.
	if !strings.Contains(text, "{{") {
		return text, nil
	}

	// Reproduce Jackett's string truthiness (!IsNullOrWhiteSpace): whitespace-only
	// values are falsy there but truthy under Go's text/template, so collapse them
	// to "" before rendering. See the .NET-truthiness contract on Context.
	normalizeWhitespaceValues(ctx)

	expanded, subs, err := expandFuncs(text, ctx)
	if err != nil {
		return "", err
	}
	// If the func phase consumed every action, skip the parser entirely — but
	// still splice each re_replace/join result back in place of its sentinel.
	if !strings.Contains(expanded, "{{") {
		return subs.splice(expanded), nil
	}

	rewritten := rewriteBadIdentRefs(expanded)

	t, err := template.New("t").Option("missingkey=zero").Parse(rewritten)
	if err != nil {
		return "", fmt.Errorf("parsing template %q: %w", text, err)
	}

	var b strings.Builder
	if err := t.Execute(&b, ctx); err != nil {
		return "", fmt.Errorf("executing template %q: %w", text, err)
	}
	// Sentinels are literal text to the stdlib engine, so they survive Execute
	// unchanged; swap them for their resolved values now that no re-parsing can
	// happen. This is what makes a re_replace/join result pure data (Jackett's
	// single-pass semantics), so brace junk in a result is emitted verbatim
	// instead of re-entering the parser and failing the whole search.
	return subs.splice(b.String()), nil
}

// substitutions records re_replace/join results and hands out a sentinel token
// to stand in for each while the template is parsed and executed.
//
// Jackett's applyGoTemplateText resolves re_replace/join in a single pass and
// treats each result as a plain VALUE: `template.Replace(all, expanded)` splices
// the result in, and only the later logic/if/range/simple-variable regexes run
// over it — the result is never re-tokenized as a fresh template. We mirror that
// by splicing a sentinel (not the raw result) into the source, parsing/executing
// with sentinels in place, then swapping sentinels for their values in the
// OUTPUT. A result carrying `{{`-shaped junk (e.g. a user keyword "foo {{bar")
// therefore never reaches the stdlib parser.
//
// The sentinel is NUL-delimited (\x00...\x00). NUL does not occur in practice in
// Cardigann definition text, tracker config values, or search keywords (all UI-
// or HTTP-query-sourced text, and Sonarr/Radarr never send it), so a token is
// neither authored by a definition nor collides with a real value, and it
// survives text/template parsing as ordinary literal text between actions. splice
// does a single left-to-right pass, so a resolved value that itself contains
// sentinel-shaped text is not re-scanned. The one contrived collision — a manual
// query hand-crafting a literal NUL sentinel (e.g. q=%00hbtok0%00) — only
// corrupts the user's own query on this single-user box; it exposes no secret and
// cannot arise from a real client. (A defensive NUL-strip on keyword input would
// make the invariant absolute; tracked as a follow-up.)
type substitutions struct {
	values []string
}

func (s *substitutions) add(value string) string {
	token := sentinelPrefix + strconv.Itoa(len(s.values)) + sentinelSuffix
	s.values = append(s.values, value)
	return token
}

func (s *substitutions) splice(out string) string {
	if len(s.values) == 0 {
		return out
	}
	pairs := make([]string, 0, len(s.values)*2)
	for i, v := range s.values {
		pairs = append(pairs, sentinelPrefix+strconv.Itoa(i)+sentinelSuffix, v)
	}
	return strings.NewReplacer(pairs...).Replace(out)
}

const (
	sentinelPrefix = "\x00hbtok"
	sentinelSuffix = "\x00"
)

// expandFuncs resolves re_replace then join (Jackett's order) by regex
// substitution, returning the template text with those actions replaced by
// sentinel tokens plus the substitutions map that maps each sentinel back to its
// resolved result.
func expandFuncs(text string, ctx *Context) (string, *substitutions, error) {
	subs := &substitutions{}
	out, err := expandReReplace(text, ctx, subs)
	if err != nil {
		return "", nil, err
	}
	return expandJoin(out, ctx, subs), subs, nil
}

// expandReReplace implements {{ re_replace .Var "pattern" "repl" }}: route the
// pattern through regexadapter (RE2 by default; regexp2 on .NET-only constructs
// or RE2 compile-failure), then Replace(resolve(.Var), repl). Mirrors Jackett's
// new Regex(pat).Replace(input, repl) with arg order variable, pattern,
// replacement. The template path carries no def language here; per-def language
// routing is applied at the engine call site, so RouteOptions is zero.
func expandReReplace(text string, ctx *Context, subs *substitutions) (string, error) {
	var firstErr error
	noteErr := func(err error) {
		if firstErr == nil {
			firstErr = err
		}
	}
	out := replaceAllSubmatch(reReplaceRe, text, func(groups []string) string {
		varPath, pattern, repl := groups[1], groups[2], groups[3]
		re, err := regexadapter.Compile(pattern, regexadapter.RouteOptions{})
		if err != nil {
			noteErr(fmt.Errorf("re_replace: %w", err))
			return ""
		}
		out, err := re.ReplaceAllString(resolveStringVar(varPath, ctx), repl)
		if err != nil {
			noteErr(fmt.Errorf("re_replace: %w", err))
			return ""
		}
		return subs.add(out)
	})
	if firstErr != nil {
		return "", firstErr
	}
	return out, nil
}

// expandJoin implements {{ join .Var "sep" }}: strings.Join(resolve(.Var), sep),
// arg order variable then separator.
func expandJoin(text string, ctx *Context, subs *substitutions) string {
	return replaceAllSubmatch(joinRe, text, func(groups []string) string {
		return subs.add(strings.Join(resolveSliceVar(groups[1], ctx), groups[2]))
	})
}

// replaceAllSubmatch applies fn to each match's submatch slice and substitutes
// the result, walking the string once.
func replaceAllSubmatch(re *regexp.Regexp, text string, fn func(groups []string) string) string {
	var b strings.Builder
	last := 0
	for _, idx := range re.FindAllStringSubmatchIndex(text, -1) {
		b.WriteString(text[last:idx[0]])
		groups := submatchStrings(text, idx)
		b.WriteString(fn(groups))
		last = idx[1]
	}
	b.WriteString(text[last:])
	return b.String()
}

func submatchStrings(text string, idx []int) []string {
	groups := make([]string, len(idx)/2)
	for i := range groups {
		start, end := idx[2*i], idx[2*i+1]
		if start >= 0 {
			groups[i] = text[start:end]
		}
	}
	return groups
}

// normalizeWhitespaceValues collapses whitespace-only string values in the
// context to "" so Go's text/template (which treats only "" as falsy) matches
// Jackett's !string.IsNullOrWhiteSpace truthiness. It mutates ctx in place; this
// is safe because callers build a fresh Context per Eval and the normalized form
// is observationally equivalent for every read path. Empty slices are already
// falsy in Go, so Categories is untouched.
func normalizeWhitespaceValues(ctx *Context) {
	normalizeMapWhitespace(ctx.Config)
	normalizeMapWhitespace(ctx.Query)
	normalizeMapWhitespace(ctx.Result)
	if strings.TrimSpace(ctx.Keywords) == "" {
		ctx.Keywords = ""
	}
}

func normalizeMapWhitespace(m map[string]string) {
	for k, v := range m {
		if v != "" && strings.TrimSpace(v) == "" {
			m[k] = ""
		}
	}
}

// resolveStringVar resolves a string-valued variable path (e.g. .Keywords,
// .Config.sort, .DownloadUri.AbsolutePath) used as a re_replace input. An
// unknown path resolves to "" — matching Jackett's missingkey=zero behavior and
// the .NET `(string)variables[var] ?? string.Empty`.
func resolveStringVar(path string, ctx *Context) string {
	switch path {
	case ".Keywords":
		return ctx.Keywords
	case ".True":
		return ctx.True
	case ".False":
		return ctx.False
	}
	if v, ok := resolveDotMap(path, ".Config.", ctx.Config); ok {
		return v
	}
	if v, ok := resolveDotMap(path, ".Query.", ctx.Query); ok {
		return v
	}
	if v, ok := resolveDotMap(path, ".Result.", ctx.Result); ok {
		return v
	}
	return resolveDownloadURIVar(path, ctx)
}

func resolveDotMap(path, prefix string, m map[string]string) (string, bool) {
	if key, ok := strings.CutPrefix(path, prefix); ok {
		return m[key], true
	}
	return "", false
}

func resolveDownloadURIVar(path string, ctx *Context) string {
	if ctx.DownloadUri == nil {
		return ""
	}
	switch path {
	case ".DownloadUri.AbsoluteUri":
		return ctx.DownloadUri.AbsoluteUri
	case ".DownloadUri.AbsolutePath":
		return ctx.DownloadUri.AbsolutePath
	case ".DownloadUri.PathAndQuery":
		return ctx.DownloadUri.PathAndQuery
	}
	if key, ok := strings.CutPrefix(path, ".DownloadUri.Query."); ok {
		return ctx.DownloadUri.Query[key]
	}
	return ""
}

// resolveSliceVar resolves a slice-valued variable path used as a join input.
// The only such path in the corpus is .Categories.
func resolveSliceVar(path string, ctx *Context) []string {
	if path == ".Categories" {
		return ctx.Categories
	}
	return nil
}

// rewriteBadIdentRefs rewrites variable references whose key segments are not
// valid Go identifiers (leading digit or dash) into index form so the stdlib
// parser accepts them, e.g.:
//
//	.Config.2facode  -> (index .Config "2facode")
//	.Config.cat-id   -> (index .Config "cat-id")
//
// Multi-segment maps chain index calls. References that are already valid Go
// (no offending segment) are left untouched.
//
// Only text inside {{ ... }} action spans is rewritten: Jackett's
// applyGoTemplateText pattern-matches variables inside actions only, and
// literal text — including values spliced in by the expandFuncs phase, such as
// a dotted keyword like "Hotel.del.Luna.2019.1080p" — must pass through
// verbatim, never mangled into (index ...) form.
//
// Action spans are located by scanning to the first "}}" after each "{{"
// rather than with a full template lexer. That would mis-terminate an action
// holding "}}" inside a quoted string (e.g. {{ "}}" }}), but no vendored
// definition does that, and a mis-split only skips a rewrite — the original
// text still reaches the stdlib parser. Text after an unclosed "{{" is left
// as literal for the parser to report.
func rewriteBadIdentRefs(text string) string {
	var b strings.Builder
	for {
		open := strings.Index(text, "{{")
		if open < 0 {
			break
		}
		span := strings.Index(text[open:], "}}")
		if span < 0 {
			break
		}
		end := open + span + 2
		b.WriteString(text[:open])
		b.WriteString(badIdentRefRe.ReplaceAllStringFunc(text[open:end], rewriteOneRef))
		text = text[end:]
	}
	b.WriteString(text)
	return b.String()
}

func rewriteOneRef(ref string) string {
	segs := strings.Split(strings.TrimPrefix(ref, "."), ".")
	if !needsIndex(segs[1:]) {
		return ref
	}
	expr := "." + segs[0]
	for _, seg := range segs[1:] {
		if isGoIdent(seg) {
			expr += "." + seg
			continue
		}
		expr = `(index ` + expr + ` "` + seg + `")`
	}
	return expr
}

func needsIndex(segs []string) bool {
	for _, seg := range segs {
		if !isGoIdent(seg) {
			return true
		}
	}
	return false
}

func isGoIdent(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		isLetter := r == '_' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
		isDigit := r >= '0' && r <= '9'
		if i == 0 && !isLetter {
			return false
		}
		if !isLetter && !isDigit {
			return false
		}
	}
	return true
}
