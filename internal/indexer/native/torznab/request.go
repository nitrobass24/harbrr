package torznab

import (
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
)

// requestLimit is the fixed page size Jackett's MoreThanTVAPI hardcodes
// (qc.Add("limit", "100")). The torznab family does not forward offset/limit
// upstream — SupportsOffsetPaging stays the native.Base default false, matching
// Jackett's request generator, which sends no offset param at all — so this is a
// constant, not query-driven like the newznab sibling's resolveLimit.
const requestLimit = 100

// Torznab t= function values. Only two named modes are ever requested (the family's
// caps advertise only tv-search/movie-search beyond plain search — commonModes); any
// other/unknown mode collapses to the bare search branch, matching Jackett's
// PerformQuery if/else-if/else chain.
const (
	fnSearch = "search"
	fnTV     = "tvsearch"
	fnMovie  = "movie"
)

// buildSearchURL maps a search.Query onto the outbound Torznab API URL:
//
//	{baseUrl}{apiPath}?t={tvsearch|movie|search}&extended=1&q=...&cat=...&imdbid=...&tvdbid=...&ep=...&season=...&limit=100&apikey=...
//
// reproducing Jackett's MoreThanTVAPI.PerformQuery exactly. Two deliberate
// divergences from the newznab sibling's buildSearchURL: there is NO
// fallback-to-search-when-no-id-param rule (t= is set directly from the query mode,
// unconditionally), and NO "+"-to-space title rewrite (q is sent trimmed, as-is) —
// Jackett's MoreThanTVAPI does neither. apikey is appended last and is
// secret-bearing; every URL this returns MUST be redacted before it is logged or
// surfaced in an error (apphttp.RedactURL / SchemeHost, used throughout get/Grab).
func (d *driver) buildSearchURL(q search.Query) string {
	params := url.Values{}
	params.Set("t", resolveMode(q.Mode))
	params.Set("extended", "1")
	if kw := strings.TrimSpace(q.Keywords); kw != "" {
		params.Set("q", kw)
	}
	if cat := joinCategories(q.Categories); cat != "" {
		params.Set("cat", cat)
	}
	if imdb := strings.TrimSpace(q.IMDBID); imdb != "" {
		params.Set("imdbid", imdb)
	}
	if tvdb := strings.TrimSpace(q.TVDBID); tvdb != "" {
		params.Set("tvdbid", tvdb)
	}
	if ep := strings.TrimSpace(q.Ep); ep != "" {
		params.Set("ep", ep)
	}
	if season, ok := positiveSeason(q.Season); ok {
		params.Set("season", season)
	}
	params.Set("limit", strconv.Itoa(requestLimit))
	// Omitted entirely when empty: AnimeTosho is a keyless public feed, and the
	// generic entry's key is optional.
	if d.apikey != "" {
		params.Set("apikey", d.apikey)
	}
	return strings.TrimRight(d.BaseURL, "/") + d.apiPath + "?" + encodeQuery(params)
}

// resolveMode maps a harbrr Query.Mode to the Torznab t= function (Jackett:
// query.IsTVSearch -> "tvsearch", query.IsMovieSearch -> "movie", else "search").
func resolveMode(mode string) string {
	switch strings.TrimSpace(mode) {
	case "tv-search":
		return fnTV
	case "movie-search":
		return fnMovie
	default:
		return fnSearch
	}
}

// positiveSeason reports the season param Jackett sends only "if query.Season > 0"
// (unlike the newznab sibling's season-zero->"00" quirk, which MoreThanTVAPI does not
// apply): a blank or non-positive season is omitted; a parseable positive season is
// re-rendered as a plain integer (matching Jackett's query.Season.ToString()). A
// non-numeric season is omitted rather than sent raw — Jackett's query.Season is
// already a validated int, harbrr's is a string param from the incoming request.
func positiveSeason(raw string) (string, bool) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", false
	}
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 {
		return "", false
	}
	return strconv.Itoa(n), true
}

// joinCategories comma-joins the resolved tracker category ids, de-duplicated and
// order-preserving (cat=2040,2050). Blank entries are dropped. Mirrors the newznab
// sibling's helper of the same shape — each driver owns its own copy rather than
// sharing a package for a five-line helper.
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

// encodeQuery encodes url.Values WITHOUT sorting so the apikey stays last and the
// param order is stable for tests (url.Values.Encode sorts by key, which would
// interleave apikey) — the same stability idiom as the newznab sibling's encodeQuery.
// A real server ignores order; harbrr controls it only for deterministic,
// redaction-safe output.
func encodeQuery(params url.Values) string {
	order := []string{"t", "extended", "q", "cat", "imdbid", "tvdbid", "ep", "season", "limit"}
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
