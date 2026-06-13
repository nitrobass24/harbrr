package torznab

import (
	"strings"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/mapper"
)

// Torznab request mode tokens (the t= query values), distinct from the
// caps.modes keys ("tv-search" etc.). Sonarr/Radarr send these.
const (
	ReqCaps     = "caps"
	ReqSearch   = "search"
	ReqTVSearch = "tvsearch"
	ReqMovie    = "movie"
	ReqMusic    = "music"
	ReqBook     = "book"
)

// searchMode binds a Torznab request token to its caps.modes key and the
// canonical supported-param order Jackett's TorznabCapabilities re-derives for
// that mode. The supportedParams output is NOT the def's raw param list: Jackett
// parses the def params into enum members then re-emits them in this fixed order
// with these canonical names (SupportedTvSearchParams etc.), so harbrr does the
// same. "q" is always first and always present.
type searchMode struct {
	req     string   // t= request token
	capsKey string   // caps.modes key
	xmlElem string   // <searching> child element name
	params  []string // canonical param order AFTER the implicit leading "q"
}

// searchModes is the ordered set of search modes, in the element order Jackett
// emits inside <searching> (search, tv-search, movie-search, music-search,
// audio-search, book-search). audio-search is a deliberate Jackett duplicate of
// music-search (Jackett #1896), so it shares music's caps key and params.
var searchModes = []searchMode{
	{req: ReqSearch, capsKey: mapper.ModeSearch, xmlElem: "search", params: nil},
	{req: ReqTVSearch, capsKey: mapper.ModeTVSearch, xmlElem: "tv-search", params: []string{"season", "ep", "imdbid", "tvdbid", "rid", "tmdbid", "tvmazeid", "traktid", "doubanid", "year", "genre"}},
	{req: ReqMovie, capsKey: mapper.ModeMovieSearch, xmlElem: "movie-search", params: []string{"imdbid", "tmdbid", "traktid", "doubanid", "year", "genre"}},
	{req: ReqMusic, capsKey: mapper.ModeMusicSearch, xmlElem: "music-search", params: []string{"album", "artist", "label", "track", "year", "genre"}},
	{req: "", capsKey: mapper.ModeMusicSearch, xmlElem: "audio-search", params: []string{"album", "artist", "label", "track", "year", "genre"}},
	{req: ReqBook, capsKey: mapper.ModeBookSearch, xmlElem: "book-search", params: []string{"title", "author", "publisher", "year", "genre"}},
}

// available reports whether the indexer advertises this mode. "search" is always
// available (Cardigann requires it; Jackett SearchAvailable defaults true); any
// other mode is available iff the definition declares it in caps.modes.
func (m searchMode) available(caps *mapper.Capabilities) bool {
	if m.capsKey == mapper.ModeSearch {
		return true
	}
	_, ok := caps.Modes[m.capsKey]
	return ok
}

// supportedParams returns the comma-joined canonical supported-param string for
// this mode, re-derived from the definition's declared params. It always starts
// with "q" and includes each canonical param the definition declared, in the
// fixed canonical order. tv-search's "imdbid" is special: Jackett gates it on
// the per-definition AllowTVSearchIMDB flag (TvSearchImdbAvailable), NOT on the
// param list, so it is included iff that flag is set.
func (m searchMode) supportedParams(caps *mapper.Capabilities) string {
	out := make([]string, 0, len(m.params)+1)
	out = append(out, "q")
	declared := declaredParamSet(caps.Modes[m.capsKey])
	for _, p := range m.params {
		if m.paramEnabled(p, declared, caps) {
			out = append(out, p)
		}
	}
	return strings.Join(out, ",")
}

// paramEnabled reports whether a single canonical param is advertised: by the
// definition's declared set in general, but tv-search "imdbid" routes through
// the AllowTVSearchIMDB flag instead (Jackett's documented quirk).
func (m searchMode) paramEnabled(param string, declared map[string]struct{}, caps *mapper.Capabilities) bool {
	if m.xmlElem == "tv-search" && param == "imdbid" {
		return caps.AllowTVSearchIMDB
	}
	_, ok := declared[param]
	return ok
}

// declaredParamSet lowercases the definition's declared param tokens into a set
// for case-insensitive membership (Jackett parses params case-insensitively via
// Enum.TryParse(..., true, ...)). The leading "q" is implicit and ignored here.
func declaredParamSet(params []string) map[string]struct{} {
	set := make(map[string]struct{}, len(params))
	for _, p := range params {
		set[strings.ToLower(strings.TrimSpace(p))] = struct{}{}
	}
	return set
}

// modeForRequest resolves a t= request token to its search mode (excluding the
// internal audio-search alias, which has no request token). Returns false for an
// unknown token.
func modeForRequest(req string) (searchMode, bool) {
	for _, m := range searchModes {
		if m.req != "" && strings.EqualFold(m.req, req) {
			return m, true
		}
	}
	return searchMode{}, false
}
