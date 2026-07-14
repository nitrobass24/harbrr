package template

import (
	"strconv"
	"time"
)

// Context is the variable namespace a Cardigann template string is evaluated
// against. The exported field names ARE the template variable names: Go's
// text/template resolves {{ .Config.foo }} by Go field name (case-sensitive),
// not by struct tag, so these names must match Jackett's variable keys exactly.
//
// Jackett mirror: GetBaseTemplateVariables / getTemplateVariablesFromConfigData
// in CardigannIndexer.cs builds a flat Dictionary<string,object> keyed by
// ".Config.<name>", ".Query.<name>", ".Today.Year", ".True", ".False", etc.
// We model the same surface as nested maps/structs so the stdlib engine can
// walk them.
//
// .NET-truthiness contract (the whole point of this stage):
//   - A MISSING key and an EMPTY string must both be falsy in {{ if .Config.x }}.
//     The maps below are string-valued and Eval sets Option("missingkey=zero"),
//     so an absent key resolves to "" — identical to an explicit "".
//   - Jackett's actual string-truthiness rule is !string.IsNullOrWhiteSpace(value)
//     (CardigannIndexer.applyGoTemplateText), so a WHITESPACE-ONLY value (" ",
//     "\t") is falsy in Jackett. Go's text/template only treats "" / empty-slice
//     as falsy, so a bare whitespace value would be truthy in Go — a divergence.
//     Eval closes this gap for {{ if .X }} conditions by normalizing whitespace-
//     only string values to "" in the context maps (and Keywords) before
//     rendering, reproducing IsNullOrWhiteSpace. This is a DELIBERATE
//     simplification, not full parity: Jackett normalizes only in conditions and
//     keeps the RAW value in interpolation and eq/ne, so a whitespace-only value
//     here also interpolates and compares as "" rather than raw — a benign,
//     degenerate edge no def or golden hits (see Eval's contract note + the parity
//     README). Empty slices are already falsy in Go (Count > 0 in Jackett), so
//     Categories needs no special handling.
//   - True/False are Jackett's sentinels: True == "True", False == "" (Jackett's
//     null). Templates compare against them, e.g. {{ if eq .Query.IMDBID .False }};
//     because an absent .Query.IMDBID also resolves to "", eq "" "" is true,
//     matching Jackett's "field is absent/empty" semantics.
type Context struct {
	// Config holds resolved settings values. The CALLER supplies these using
	// Jackett's encoding (getTemplateVariablesFromConfigData) so that bare
	// {{ if .Config.x }} truthiness matches:
	//   - checkbox: unchecked => "" (falsy), checked => "True" (non-empty/truthy)
	//   - text/password: the raw string value (default or user-entered)
	//   - select: the selected option's value string
	//   - multi-select: Jackett joins/exposes a list; for the cases the corpus
	//     exercises a value string is sufficient here. Multi-select join lives
	//     with the caller, not this stage.
	// Jackett also seeds .Config.sitelink; the caller populates it the same way.
	Config map[string]string

	// Query holds the parsed search query fields (Keywords, IMDBID, Season,
	// Episode, Artist, ...). Absent fields resolve to "" via missingkey=zero,
	// which is what makes the eq .Query.IMDBID .False idiom work.
	Query map[string]string

	// Keywords is the top-level {{ .Keywords }} convenience variable (the search
	// term), distinct from {{ .Query.Keywords }}.
	Keywords string

	// Categories is the resolved tracker category list, the target of
	// {{ join .Categories "," }} and {{ range .Categories }}.
	Categories []string

	// Result holds per-row result variables available while building download
	// requests ({{ .Result.foo }}).
	Result map[string]string

	// Today exposes {{ .Today.Year }} / {{ .Today.Month }} / {{ .Today.Day }}.
	Today Today

	// DownloadUri exposes the request URI members used by download/before
	// templates, e.g. {{ .DownloadUri.Query.id }} and
	// {{ re_replace .DownloadUri.AbsolutePath "/info/" "" }}.
	//
	// PRECONDITION: the caller MUST populate DownloadUri before evaluating any
	// template that references it (i.e. download/before templates). When it is
	// nil the two evaluation paths diverge: the re_replace pre-pass nil-guards
	// and yields "" (resolveDownloadURIVar), but a bare {{ .DownloadUri.X }} is
	// handed to the stdlib parser, which hard-errors on a nil-pointer member.
	// Because download templates only ever run with a real download URI, the
	// engine satisfies this precondition; the asymmetry is acceptable
	// only under it.
	//
	// The field name MUST stay "DownloadUri" (not the Go-idiomatic "DownloadURI"):
	// {{ .DownloadUri.Query.id }} is resolved by the stdlib parser via Go field
	// name, so it must match the corpus variable key byte-for-byte.
	DownloadUri *DownloadURI //nolint:revive // name mirrors the corpus template variable key

	// True and False are Jackett's comparison sentinels. NewContext sets
	// True = "True" and False = "".
	True  string
	False string
}

// DownloadURI mirrors the .NET System.Uri members that download/before
// templates reference. All member names are load-bearing: the corpus uses both
// bare interpolations ({{ .DownloadUri.AbsoluteUri }}, {{ .DownloadUri.Query.id }})
// resolved by the stdlib parser via Go field name, and re_replace inputs
// (.DownloadUri.AbsolutePath, .DownloadUri.PathAndQuery) resolved by the
// pre-pass. They therefore mirror the corpus variable keys byte-for-byte rather
// than taking Go-idiomatic initialisms.
//
//nolint:revive // member names mirror the corpus template variable keys
type DownloadURI struct {
	AbsoluteUri  string
	AbsolutePath string
	PathAndQuery string
	Query        map[string]string
}

// Today mirrors Jackett's .Today.* variables. Fields are strings because that
// is how they are interpolated into URLs.
type Today struct {
	Year  string
	Month string
	Day   string
}

// NewContext returns a Context with the True/False sentinels set and every map
// initialized, so callers and templates can index them without nil-map panics.
// Most callers want NewSeeded instead; NewContext is the bare building block
// tests reach for when they want to hand-mutate individual fields.
func NewContext() *Context {
	return &Context{
		Config: map[string]string{},
		Query:  map[string]string{},
		Result: map[string]string{},
		True:   "True",
		False:  "",
	}
}

// Params groups the typed inputs used to seed a fresh Context for one
// template evaluation. Each field maps directly onto the Context member of
// the same purpose; a zero value is fine and matches that member's own
// zero-value semantics (a nil Query/Result map still renders "" for any key
// via missingkey=zero, a nil Clock leaves .Today unset — see NewSeeded).
type Params struct {
	// Config seeds .Config. BaseURL backs the .Config.sitelink default:
	// NewSeeded sets it only when Config carries no "sitelink" key of its
	// own, matching Jackett's GetBaseTemplateVariables seeding.
	Config  map[string]string
	BaseURL string

	// Query seeds .Query (the search package builds this from its own Query
	// type via queryMap()).
	Query map[string]string

	// Result seeds the growing per-row .Result map. Leave nil for a context
	// that only renders request/login templates, which never reference it.
	Result map[string]string

	// Keywords seeds the top-level .Keywords convenience variable.
	Keywords string

	// Categories seeds .Categories.
	Categories []string

	// Clock supplies the reference time for .Today (the January-rollover
	// quirk; see today). Nil leaves .Today at its zero value (every field
	// ""), which is what a caller that never renders .Today — login — wants.
	// A caller that DOES want .Today must resolve its own nil-clock default
	// before calling NewSeeded (the search package falls back to time.Now,
	// matching Deps.Clock's documented contract).
	Clock func() time.Time

	// DownloadURI seeds .DownloadUri for download/before templates. Nil for
	// every other template (see Context.DownloadUri's precondition).
	DownloadURI *DownloadURI
}

// NewSeeded returns a ready Context built from p: .Config.sitelink defaulted
// from p.BaseURL when Config carries none, .Today computed from p.Clock with
// Jackett's January-rollover quirk (nil Clock leaves .Today unset), and the
// rest of the namespace copied in directly.
//
// Call NewSeeded FRESH for every template.Eval — never share or reuse the
// returned Context across evaluations. Eval mutates it in place (whitespace
// normalization), so a cached or reused Context corrupts a later evaluation.
func NewSeeded(p Params) *Context {
	ctx := NewContext()
	for k, v := range p.Config {
		ctx.Config[k] = v
	}
	if _, ok := ctx.Config["sitelink"]; !ok {
		ctx.Config["sitelink"] = p.BaseURL
	}
	for k, v := range p.Query {
		ctx.Query[k] = v
	}
	for k, v := range p.Result {
		ctx.Result[k] = v
	}
	ctx.Keywords = p.Keywords
	ctx.Categories = p.Categories
	if p.Clock != nil {
		ctx.Today = today(p.Clock)
	}
	ctx.DownloadUri = p.DownloadURI
	return ctx
}

// today renders the .Today namespace from the reference clock. Jackett seeds
// .Today.Year/Month/Day from DateTime.Today (GetBaseTemplateVariables); the
// engine injects a deterministic clock so date-defaulting templates are
// reproducible.
//
// Jackett applies a deliberate quirk to .Today.Year: in January (month == 1)
// it reports the PREVIOUS year — `Month > 1 ? Year : Year - 1` — so a def
// that defaults a missing date to "{{ .Today.Year }}-01-01" does not stamp a
// just-rolled-over release in the future. We reproduce it exactly for parity.
func today(clock func() time.Time) Today {
	now := clock()
	year := now.Year()
	if now.Month() == time.January {
		year--
	}
	return Today{
		Year:  strconv.Itoa(year),
		Month: strconv.Itoa(int(now.Month())),
		Day:   strconv.Itoa(now.Day()),
	}
}
