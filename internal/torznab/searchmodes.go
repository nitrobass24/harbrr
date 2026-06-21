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
// other mode is available iff the definition declares it with at least one param,
// matching Jackett's XxxSearchAvailable => XxxSearchParams.Count > 0 (a
// declared-but-empty list like `movie-search: []` is not available).
func (m searchMode) available(caps *mapper.Capabilities) bool {
	if m.capsKey == mapper.ModeSearch {
		return true
	}
	return len(caps.Modes[m.capsKey]) > 0
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

// CapabilityTokens flattens the indexer's advertised <searching> caps into the
// flat token list autobrr/qui stores per Torznab indexer: each available mode's
// element name plus "<mode>-<param>" for every supported param (which always
// leads with "q"), e.g. "tv-search", "tv-search-q", "tv-search-season",
// "tv-search-imdbid", "movie-search", "movie-search-imdbid". harbrr pushes these
// to qui directly because qui's "native" backend cannot fetch caps from a full
// feed URL (its go-jackett client malforms the request — see docs/external-issues.md).
// The set matches what qui derives from the caps XML; order is the caps element
// order (qui sorts for display).
func CapabilityTokens(caps *mapper.Capabilities) []string {
	if caps == nil {
		return nil
	}
	out := make([]string, 0, 16)
	for _, m := range searchModes {
		if !m.available(caps) {
			continue
		}
		out = append(out, m.xmlElem)
		for _, p := range strings.Split(m.supportedParams(caps), ",") {
			out = append(out, m.xmlElem+"-"+p)
		}
	}
	return out
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

// modeByCapsKey resolves a caps.modes key to its search mode.
func modeByCapsKey(capsKey string) (searchMode, bool) {
	for _, m := range searchModes {
		if m.capsKey == capsKey {
			return m, true
		}
	}
	return searchMode{}, false
}

// ModeForRequest resolves a Torznab t= token (search, tvsearch, movie, music,
// book) to its caps.modes key for the request handler. ok is false for an
// unknown token (the caller handles t=caps separately and rejects others).
func ModeForRequest(t string) (capsKey string, ok bool) {
	m, found := modeForRequest(t)
	if !found {
		return "", false
	}
	return m.capsKey, true
}

// ModeAvailable reports whether the indexer's capabilities advertise the mode
// identified by capsKey (the request handler rejects an unadvertised mode).
func ModeAvailable(caps *mapper.Capabilities, capsKey string) bool {
	m, ok := modeByCapsKey(capsKey)
	return ok && m.available(caps)
}

// SupportsParam reports whether the mode identified by capsKey advertises the
// canonical Torznab param (e.g. "imdbid", "tmdbid", "tvdbid"), so the request
// handler can reject an id search the indexer does not support (error 203)
// instead of silently degrading it to a keyword search.
func SupportsParam(caps *mapper.Capabilities, capsKey, param string) bool {
	m, ok := modeByCapsKey(capsKey)
	if !ok {
		return false
	}
	return m.paramEnabled(param, declaredParamSet(caps.Modes[capsKey]), caps)
}
