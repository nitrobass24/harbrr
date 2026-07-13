package search

import (
	"fmt"
	"regexp"
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
	// so every Cardigann request URL stays byte-identical: paging-capable native drivers
	// (newznab, nzbindex) forward them upstream for deep-set paging, while non-paging drivers
	// leave them for the handler to slice the returned page. A zero Offset/Limit means "first
	// page, default size".
	Offset int
	Limit  int

	// FreeleechBypass requests the full catalog from harbrr's serve-time freeleech view
	// (the freeleech-bypass feed variant, for qui/cross-seed). It is REQUEST CONTEXT for
	// the registry adapter's freeleech serve-time view only: the Cardigann engine never
	// templates it (the engine always fetches the full catalog regardless), and it is
	// deliberately NOT part of the search-cache key — honor and bypass share one cached
	// full-set entry, and the adapter narrows it post-cache. See
	// website/docs/features/cross-seed-freeleech.md.
	FreeleechBypass bool

	// keywordsFiltered, when non-nil, is the joined keyword term after the
	// definition's search.keywordsfilters ran over it. Set by applyKeywordsFilters
	// at the executor entry points; nil means the definition declares no
	// keywordsfilters. templateKeywords reads it; .Query.Keywords always stays raw
	// (queryMap), matching Jackett, which filters only the .Keywords variable.
	keywordsFiltered *string
}

// isIDSearch reports whether any ID-style param is set. Jackett skips the
// andmatch row filter for id searches (ParseRowFilters), so the engine consults
// this to decide whether to apply andmatch.
func (q Query) isIDSearch() bool {
	return q.IMDBID != "" || q.TMDBID != "" || q.TVDBID != "" ||
		q.TVMazeID != "" || q.TraktID != "" || q.DoubanID != "" || q.RageID != ""
}

// keywords reproduces Jackett's KeywordTokens join (PerformQuery): Q, Series,
// Movie, Year, then the formatted episode string, single-space-joined with
// empty tokens skipped. harbrr models Keywords as the already-joined Q term and
// Jackett initializes .Query.Series/.Query.Movie to null, so the effective
// tokens are Keywords + Year + episodeSearchString. This joined value is what
// keywordsfilters run over — the episode token is part of the filtered base.
func (q Query) keywords() string {
	tokens := make([]string, 0, 3)
	if t := strings.TrimSpace(q.Keywords); t != "" {
		tokens = append(tokens, t)
	}
	if y := strings.TrimSpace(q.Year); y != "" {
		tokens = append(tokens, y)
	}
	if e := q.episodeSearchString(); e != "" {
		tokens = append(tokens, e)
	}
	return strings.Join(tokens, " ")
}

// episodeSearchString reproduces Jackett's TorznabQuery.GetEpisodeSearchString,
// the formatted season/episode token joined into KeywordTokens and exposed as
// .Query.Episode. An absent or zero season yields "" (even with an episode
// set); a daily search — season carries the four-digit year, ep is "MM/dd" —
// renders "yyyy.MM.dd"; a season alone renders "S%02d"; a numeric episode
// renders "S%02dE%02d". A non-numeric episode is appended raw ("S03E2v2") —
// a deliberate, benign divergence from Jackett, whose ParseUtil.CoerceInt
// digit-strips ("2v2" -> 22) before formatting; both branches are unreachable
// from a real torznab client (Sonarr/Prowlarr send numeric episodes), and
// Jackett's stripped output is meaningless. Jackett's Season is an int? coerced
// at request parse, so a non-numeric season string means no season there —
// treat it as absent.
func (q Query) episodeSearchString() string {
	season, err := strconv.Atoi(strings.TrimSpace(q.Season))
	if err != nil || season == 0 {
		return ""
	}
	ep := strings.TrimSpace(q.Ep)
	if date, ok := dailyEpisodeDate(season, ep); ok {
		return date
	}
	if ep == "" {
		return fmt.Sprintf("S%02d", season)
	}
	if epNum, err := strconv.Atoi(ep); err == nil {
		return fmt.Sprintf("S%02dE%02d", season, epNum)
	}
	return fmt.Sprintf("S%02dE%s", season, ep)
}

// dailyEpisodePattern guards the daily-episode parse with ParseExact's fixed
// digit widths ("yyyy MM/dd"), which Go's lenient time.Parse would otherwise
// relax (e.g. accepting a single-digit month Jackett rejects).
var dailyEpisodePattern = regexp.MustCompile(`^\d{4} \d{2}/\d{2}$`)

// dailyEpisodeDate reproduces Jackett's
// DateTime.TryParseExact($"{Season} {Episode}", "yyyy MM/dd") branch: a daily
// show search renders "yyyy.MM.dd" instead of an SxxExx token.
func dailyEpisodeDate(season int, ep string) (string, bool) {
	joined := strconv.Itoa(season) + " " + ep
	if !dailyEpisodePattern.MatchString(joined) {
		return "", false
	}
	d, err := time.Parse("2006 01/02", joined)
	if err != nil {
		return "", false
	}
	return d.Format("2006.01.02"), true
}

// templateKeywords is the value the top-level .Keywords template variable and
// the andmatch row filter read: the keywordsfilters-filtered term when the
// definition declares filters (applyKeywordsFilters), the raw joined term
// otherwise. Jackett sets variables[".Keywords"] to the filtered value before
// request templating, and its andmatch reads the same variable.
func (q Query) templateKeywords() string {
	if q.keywordsFiltered != nil {
		return *q.keywordsFiltered
	}
	return q.keywords()
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
	// .Query.Season is nulled for season 0 (Jackett: query.Season > 0 ? ... :
	// null — a Sonarr specials search templates ""); .Query.Ep stays raw while
	// .Query.Episode is the FORMATTED episode string, matching Jackett's
	// variables[".Query.Episode"] = query.GetEpisodeSearchString().
	if q.Season != "0" {
		set("Season", q.Season)
	}
	set("Ep", q.Ep)
	set("Episode", q.episodeSearchString())
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
