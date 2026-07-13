package template

import "net/url"

// NewDownloadURI builds the .DownloadUri template namespace from the link the
// download resolver is processing, mirroring Jackett's AddTemplateVariablesFromUri
// (CardigannIndexer.cs): each member maps a Go *url.URL onto its .NET System.Uri
// counterpart, and .Query is QueryHelpers.ParseQuery's first-value-per-key.
//
//   - AbsoluteUri  -> u.String()
//   - AbsolutePath -> u.EscapedPath() (normalized to "/" when empty, matching .NET,
//     which never yields an empty AbsolutePath for an http(s) URI)
//   - PathAndQuery -> u.RequestURI()  ("/" path default already matches .NET)
//   - Query        -> first value per key (the corpus reads .DownloadUri.Query.id
//     as a single string; first-value matches ParseQuery(...).First())
//
// This is exact for every member real defs read (`{{ .DownloadUri.Query.<k> }}`
// and `re_replace .DownloadUri.AbsolutePath ...` over the URL shapes the corpus
// actually produces — bare `path?id=NNN` / `/info/NNN`). It deliberately does NOT
// reproduce .NET's Uri *canonicalization* that no corpus def exercises: stripping
// a default :80/:443, lowercasing the host, compacting dot-segments, or unescaping
// percent-encoded unreserved octets in the path. Those exotic shapes are tracked
// as an engine divergence rather than half-modelled here; a def that needs them
// routes through the existing encode/regex layers.
//
// PRECONDITION: the caller passes a resolved, absolute http(s) URL and satisfies
// the Context.DownloadUri precondition by assigning the result before evaluating
// any download/before template. A nil, relative, or opaque (magnet:) URL is a
// caller bug — the resolver only ever holds an absolute link here.
func NewDownloadURI(u *url.URL) *DownloadURI {
	absPath := u.EscapedPath()
	if absPath == "" {
		absPath = "/"
	}
	query := make(map[string]string, len(u.Query()))
	for key, vals := range u.Query() {
		if len(vals) > 0 {
			query[key] = vals[0]
		}
	}
	return &DownloadURI{
		AbsoluteUri:  u.String(),
		AbsolutePath: absPath,
		PathAndQuery: u.RequestURI(),
		Query:        query,
	}
}
