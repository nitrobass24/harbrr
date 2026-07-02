package search

import (
	"strconv"
	"strings"
	"time"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/template"
)

// Query is the parsed search request the engine drives a definition with. It is
// the harbrr-internal equivalent of Jackett's TorznabQuery, reduced to the
// fields the request-building and row-filter stages read. Empty fields render to
// "" in templates (matching Jackett's null-to-empty coercion), so a zero Query is
// a valid "raw RSS" search.
type Query struct {
	// Keywords is the free-text search term (Jackett .Query.Q / .Keywords).
	Keywords string
	// Categories is the resolved tracker category id list ({{ .Categories }}).
	Categories []string

	// ID-style query params. A non-empty value here both feeds the request
	// templates and gates the andmatch row filter off (Jackett skips andmatch for
	// id searches), matching ParseRowFilters.
	IMDBID   string
	TMDBID   string
	TVDBID   string
	TVMazeID string
	TraktID  string
	DoubanID string
	RageID   string

	// Episode/series params.
	Season string
	Ep     string
	Year   string

	// Music/book params.
	Artist    string
	Album     string
	Label     string
	Track     string
	Author    string
	BookTitle string

	// Mode is the Torznab search mode (the caps key — "search", "tv-search",
	// "movie-search", "music-search", "book-search") the caller resolved from the
	// request's t= param. It is request context, not a tracker request param: the
	// Cardigann engine never templates it (queryMap maps only the named fields above).
	// A native driver may read it to pick a search namespace it can't infer from the
	// fields alone (AnimeBytes routes music-search to its music corpus). It IS part of
	// the search-cache key, since for such a driver the mode changes the outbound
	// request. Empty means a general/unspecified search (treated as "search").
	Mode string

	// Offset and Limit are REQUEST CONTEXT — the served page window — never templated.
	// The Cardigann engine ignores them entirely (queryMap does not map them, like Mode),
	// so every Cardigann request URL stays byte-identical: a paging-capable native driver
	// (newznab) forwards them upstream for deep-set paging, while non-paging drivers leave
	// them for the handler to slice the returned page. A zero Offset/Limit means "first
	// page, default size".
	Offset int
	Limit  int

	// FreeleechBypass requests the full catalog from harbrr's serve-time freeleech view
	// (the freeleech-bypass feed variant, for qui/cross-seed). It is REQUEST CONTEXT for
	// the registry's freeleechIndexer decorator only: the Cardigann engine never templates
	// it (the engine always fetches the full catalog regardless), and it is deliberately
	// NOT part of the search-cache key — honor and bypass share one cached full-set entry,
	// and the decorator narrows it post-cache. See docs/plan.md Phase-11.
	FreeleechBypass bool
}

// isIDSearch reports whether any ID-style param is set. Jackett skips the
// andmatch row filter for id searches (ParseRowFilters), so the engine consults
// this to decide whether to apply andmatch.
func (q Query) isIDSearch() bool {
	return q.IMDBID != "" || q.TMDBID != "" || q.TVDBID != "" ||
		q.TVMazeID != "" || q.TraktID != "" || q.DoubanID != "" || q.RageID != ""
}

// keywords reproduces Jackett's KeywordTokens join: Q + Series + Movie + Year +
// Episode, whitespace-joined. harbrr models Keywords as the already-joined term,
// so this simply trims; the Year token is appended when present (the only extra
// token the corpus request templates rely on beyond the bare term).
func (q Query) keywords() string {
	tokens := make([]string, 0, 2)
	if t := strings.TrimSpace(q.Keywords); t != "" {
		tokens = append(tokens, t)
	}
	if y := strings.TrimSpace(q.Year); y != "" {
		tokens = append(tokens, y)
	}
	return strings.Join(tokens, " ")
}

// queryMap renders the Query fields into the .Query.<name> template namespace,
// using Jackett's exact variable keys so request templates resolve identically.
// Absent fields are simply not set, so missingkey=zero makes them "" / falsy.
func (q Query) queryMap() map[string]string {
	m := map[string]string{}
	set := func(k, v string) {
		if v != "" {
			m[k] = v
		}
	}
	set("Q", q.Keywords)
	set("Keywords", q.keywords())
	set("IMDBID", q.IMDBID)
	set("TMDBID", q.TMDBID)
	set("TVDBID", q.TVDBID)
	set("TVMazeID", q.TVMazeID)
	set("TraktID", q.TraktID)
	set("DoubanID", q.DoubanID)
	set("TVRageID", q.RageID)
	set("Season", q.Season)
	set("Ep", q.Ep)
	set("Episode", q.Ep)
	set("Year", q.Year)
	set("Artist", q.Artist)
	set("Album", q.Album)
	set("Label", q.Label)
	set("Track", q.Track)
	set("Author", q.Author)
	set("Title", q.BookTitle)
	// Offset/Limit are intentionally NOT mapped (like Mode): they are request context,
	// not tracker request params, so the Cardigann request URL stays byte-identical.
	q.setQueryFlags(m)
	return m
}

// setQueryFlags reproduces Jackett's boolean .Query.Is* sentinels that request
// templates branch on (e.g. {{ if .Query.IsImdbQuery }}). A true flag is the
// string "True"; a false flag is left absent (Jackett sets it to null), so
// missingkey=zero renders it "" / falsy.
func (q Query) setQueryFlags(m map[string]string) {
	if q.IMDBID != "" {
		m["IsImdbQuery"] = trueSentinel
	}
	if q.TMDBID != "" {
		m["IsTmdbQuery"] = trueSentinel
	}
	if q.TVDBID != "" {
		m["IsTvdbQuery"] = trueSentinel
	}
	if q.isIDSearch() {
		m["IsIdSearch"] = trueSentinel
	}
}

// trueSentinel mirrors Jackett's "True" string used for boolean query flags.
const trueSentinel = "True"

// newContext builds a fresh template.Context for one Eval. A fresh context per
// call is required because template.Eval mutates it (whitespace normalization).
// config supplies the .Config namespace (and .Config.sitelink); query supplies
// .Query / .Keywords / .Categories; result seeds the growing per-row .Result map;
// clock seeds the .Today namespace ({{ .Today.Year }}). A nil clock defaults to
// time.Now so .Today is never silently empty.
func newContext(config, query, result map[string]string, keywords string, categories []string, clock func() time.Time) *template.Context {
	ctx := template.NewContext()
	for k, v := range config {
		ctx.Config[k] = v
	}
	for k, v := range query {
		ctx.Query[k] = v
	}
	for k, v := range result {
		ctx.Result[k] = v
	}
	ctx.Keywords = keywords
	ctx.Categories = categories
	ctx.Today = today(clock)
	return ctx
}

// today renders the .Today namespace from the reference clock. Jackett seeds
// .Today.Year/Month/Day from DateTime.Today (GetBaseTemplateVariables); the
// engine injects a deterministic clock so date-defaulting templates are
// reproducible.
//
// Jackett applies a deliberate quirk to .Today.Year: in January (month == 1) it
// reports the PREVIOUS year — `Month > 1 ? Year : Year - 1` — so a def that
// defaults a missing date to "{{ .Today.Year }}-01-01" does not stamp a
// just-rolled-over release in the future. We reproduce it exactly for parity.
func today(clock func() time.Time) template.Today {
	if clock == nil {
		clock = time.Now
	}
	now := clock()
	year := now.Year()
	if now.Month() == time.January {
		year--
	}
	return template.Today{
		Year:  strconv.Itoa(year),
		Month: strconv.Itoa(int(now.Month())),
		Day:   strconv.Itoa(now.Day()),
	}
}
