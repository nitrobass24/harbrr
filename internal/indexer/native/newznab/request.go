package newznab

import (
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
)

// defaultLimit is the page size harbrr requests when the query carries no explicit limit
// (Prowlarr NewznabRequestGenerator PageSize=100). The Newznab API takes offset/limit, so
// the driver forwards the requested page window upstream (SupportsOffsetPaging) for
// deep-set paging rather than fetching only the first 100 and slicing downstream.
const defaultLimit = 100

// Newznab t= function values per the v1.3 spec (NewznabRequestGenerator). Note the
// asymmetry: TV is "tvsearch" but movie/music/book are bare nouns.
const (
	fnSearch = "search"
	fnTV     = "tvsearch"
	fnMovie  = "movie"
	fnMusic  = "music"
	fnBook   = "book"
)

// buildSearchURL maps a search.Query onto the outbound Newznab API URL:
//
//	{baseUrl}{apiPath}?t={fn}&extended=1&q=...&cat=...&{id params}&limit=100&apikey=...
//
// Both baseUrl and apiPath are already right-trimmed of "/". extended=1 is always sent so
// the response carries the newznab:attr fields the parser reads. The function (t=) is
// resolved per mode with Prowlarr's fallback-to-search rule: a mode-specific search with NO
// mode-specific param (no ids / artist / album / author / title) resets to t=search and
// sends only q. apikey is appended last and is secret-bearing — it MUST be redacted before
// any URL is logged or surfaced in an error.
func (d *driver) buildSearchURL(q search.Query) string {
	params := url.Values{}
	fn := d.fillModeParams(params, q)
	params.Set("t", fn)
	params.Set("extended", "1")
	if kw := newsnabifyTitle(q.Keywords); kw != "" {
		params.Set("q", kw)
	}
	if cat := joinCategories(q.Categories); cat != "" {
		params.Set("cat", cat)
	}
	// Forward the requested page window upstream. offset is emitted ONLY when > 0, so a
	// first-page (offset=0) request stays byte-identical to the pre-paging wire form; limit
	// falls back to the 100-result default when the query carries none.
	if q.Offset > 0 {
		params.Set("offset", strconv.Itoa(q.Offset))
	}
	params.Set("limit", strconv.Itoa(resolveLimit(q.Limit)))
	if d.apikey != "" {
		params.Set("apikey", d.apikey)
	}
	return d.baseURL + d.apiPath + "?" + encodeQuery(params)
}

// resolveLimit picks the upstream page size: the query's explicit limit when positive,
// else the 100-result default. A non-positive limit (a zero Query, or a caller that left
// it unset) falls back so a bare RSS search keeps fetching a full page.
func resolveLimit(limit int) int {
	if limit > 0 {
		return limit
	}
	return defaultLimit
}

// fillModeParams sets the mode-specific id/criteria params on params and returns the
// resolved t= function. It reproduces Prowlarr's per-mode generators and the
// fallback-to-search rule: when the requested mode carries no mode-specific param, the
// function collapses to "search" and only q/cat are sent.
func (d *driver) fillModeParams(params url.Values, q search.Query) string {
	switch normalizeMode(q.Mode) {
	case fnTV:
		if d.fillTVParams(params, q) {
			return fnTV
		}
	case fnMovie:
		if d.fillMovieParams(params, q) {
			return fnMovie
		}
	case fnMusic:
		if fillMusicParams(params, q) {
			return fnMusic
		}
	case fnBook:
		if fillBookParams(params, q) {
			return fnBook
		}
	}
	return fnSearch
}

// fillTVParams sets the tv-search id/episode params (imdbid digits-only, tvdbid, tvmazeid,
// rid, season with the 0->"00" quirk, ep) and reports whether any was set. traktid is movie-only
// in Prowlarr's generator, so it is intentionally not sent here.
func (d *driver) fillTVParams(params url.Values, q search.Query) bool {
	set := false
	set = setParam(params, "imdbid", imdbDigits(q.IMDBID)) || set
	set = setParam(params, "tvdbid", digits(q.TVDBID)) || set
	set = setParam(params, "tvmazeid", digits(q.TVMazeID)) || set
	set = setParam(params, "rid", digits(q.RageID)) || set
	set = setParam(params, "season", newznabifySeason(q.Season)) || set
	set = setParam(params, "ep", strings.TrimSpace(q.Ep)) || set
	return set
}

// fillMovieParams sets the movie-search id params (imdbid digits-only, tmdbid, traktid) and
// reports whether any was set.
func (d *driver) fillMovieParams(params url.Values, q search.Query) bool {
	set := false
	set = setParam(params, "imdbid", imdbDigits(q.IMDBID)) || set
	set = setParam(params, "tmdbid", digits(q.TMDBID)) || set
	set = setParam(params, "traktid", digits(q.TraktID)) || set
	return set
}

// fillMusicParams sets the music-search params (artist, album) and reports whether any was
// set.
func fillMusicParams(params url.Values, q search.Query) bool {
	set := false
	set = setParam(params, "artist", strings.TrimSpace(q.Artist)) || set
	set = setParam(params, "album", strings.TrimSpace(q.Album)) || set
	return set
}

// fillBookParams sets the book-search params (author, and title from BookTitle — the
// Newznab book param is "title", NOT q) and reports whether any was set.
func fillBookParams(params url.Values, q search.Query) bool {
	set := false
	set = setParam(params, "author", strings.TrimSpace(q.Author)) || set
	set = setParam(params, "title", strings.TrimSpace(q.BookTitle)) || set
	return set
}

// setParam sets key=value when value is non-empty, returning whether it set anything.
func setParam(params url.Values, key, value string) bool {
	if value == "" {
		return false
	}
	params.Set(key, value)
	return true
}

// normalizeMode maps a harbrr Query.Mode (the caps key) to the Newznab t= function. An
// empty/unknown mode is a basic search.
func normalizeMode(mode string) string {
	switch strings.TrimSpace(mode) {
	case "tv-search":
		return fnTV
	case "movie-search":
		return fnMovie
	case "music-search":
		return fnMusic
	case "book-search":
		return fnBook
	default:
		return fnSearch
	}
}

// newsnabifyTitle reproduces Prowlarr's NewsnabifyTitle: strip "+" from the raw term
// (so a literal "+" is not carried through as if it were an encoded space) by replacing
// it with a literal space. encodeQuery then escapes that space via url.QueryEscape, which
// emits "+" on the wire — the x-www-form-urlencoded space form, not "%20".
func newsnabifyTitle(raw string) string {
	return strings.ReplaceAll(strings.TrimSpace(raw), "+", " ")
}

// joinCategories comma-joins the resolved tracker category ids, de-duplicated and order-
// preserving (cat=2000,2010). Blank entries are dropped.
func joinCategories(cats []string) string {
	seen := make(map[string]struct{}, len(cats))
	out := make([]string, 0, len(cats))
	for _, c := range cats {
		c = strings.TrimSpace(c)
		if c == "" {
			continue
		}
		if _, dup := seen[c]; dup {
			continue
		}
		seen[c] = struct{}{}
		out = append(out, c)
	}
	return strings.Join(out, ",")
}

// imdbDigits renders an imdb id as the bare digits Newznab expects: a leading "tt" is
// stripped (Prowlarr's NewznabController TrimStart('t')); a non-digit/empty value yields "".
func imdbDigits(raw string) string {
	s := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(raw)), "tt")
	return digits(s)
}

// digits returns s when it is a non-empty run of decimal digits, else "". It guards the
// integer id params (tmdbid/tvdbid/…) so a malformed id is dropped rather than sent raw.
func digits(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return ""
		}
	}
	return s
}

// newznabifySeason reproduces Prowlarr's NewznabifySeasonNumber: season 0 -> "00", else the
// integer as-is. A blank season yields "" (omitted). A non-numeric season is passed through
// trimmed (Prowlarr formats an int, but harbrr's Season is a string).
func newznabifySeason(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ""
	}
	if n, err := strconv.Atoi(s); err == nil && n == 0 {
		return "00"
	}
	return s
}

// encodeQuery encodes url.Values WITHOUT sorting so the apikey stays last and the param
// order is stable for tests (url.Values.Encode sorts by key, which would interleave apikey).
// A real server ignores order; harbrr controls it only for deterministic, redaction-safe
// output.
func encodeQuery(params url.Values) string {
	// Stable cosmetic order: t, extended, then the rest in a fixed sequence. Any param
	// not in this list is appended in sorted order so a future key is never silently
	// dropped; apikey is always emitted last (redaction-stable).
	order := []string{
		"t", "extended", "q", "cat",
		"imdbid", "tmdbid", "tvdbid", "tvmazeid", "rid", "traktid",
		"season", "ep", "artist", "album", "author", "title",
		"offset", "limit",
	}
	known := map[string]bool{"apikey": true}
	for _, k := range order {
		known[k] = true
	}
	var extra []string
	for k := range params {
		if !known[k] {
			extra = append(extra, k)
		}
	}
	sort.Strings(extra)

	var b strings.Builder
	first := true
	write := func(key string) {
		for _, v := range params[key] {
			if !first {
				b.WriteByte('&')
			}
			first = false
			b.WriteString(url.QueryEscape(key))
			b.WriteByte('=')
			b.WriteString(url.QueryEscape(v))
		}
	}
	for _, key := range order {
		write(key)
	}
	for _, key := range extra {
		write(key)
	}
	write("apikey")
	return b.String()
}
