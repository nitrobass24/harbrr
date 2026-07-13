package template

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
func NewContext() *Context {
	return &Context{
		Config: map[string]string{},
		Query:  map[string]string{},
		Result: map[string]string{},
		True:   "True",
		False:  "",
	}
}
