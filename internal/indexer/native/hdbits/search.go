package hdbits

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	stdhttp "net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/login"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/normalizer"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
)

// searchLimit is the page size harbrr requests. Prowlarr hardcodes query.Limit=100 in
// HDBitsRequestGenerator.GetRequest regardless of caller; harbrr fetches one page and
// paginates response-side downstream, so page is always 0 (Prowlarr sets page only when
// offset/limit are both > 0).
const searchLimit = 100

// nonWordRun matches Prowlarr's Regex.Replace(term, "[\W]+", " "): one or more
// non-word characters collapse to a single space when sanitizing a MOVIE search term.
var nonWordRun = regexp.MustCompile(`[\W]+`)

// torrentQuery is the JSON POST body HDBits' api/torrents expects (Prowlarr's
// TorrentQuery). Username and passkey are top-level fields (both Required Always in
// Prowlarr), so the ENTIRE marshalled body is secret-bearing and never logged. Every
// optional field is omitempty so an unset key is dropped (matching Prowlarr's
// DefaultValueHandling.Ignore): a bare browse query marshals to just the credentials and
// the limit.
type torrentQuery struct {
	Username string     `json:"username"`
	Passkey  string     `json:"passkey"`
	Search   string     `json:"search,omitempty"`
	Category []int      `json:"category,omitempty"`
	Imdb     *imdbQuery `json:"imdb,omitempty"`
	Tvdb     *tvdbQuery `json:"tvdb,omitempty"`
	Limit    int        `json:"limit"`
	Page     int        `json:"page,omitempty"`
}

// imdbQuery is the body's imdb object: the bare numeric id (tt-stripped), matching
// Prowlarr's TorrentQuery.ImdbInfo.
type imdbQuery struct {
	ID int `json:"id"`
}

// tvdbQuery is the body's tvdb object: the series id and, for a standard episode query,
// the season int and the episode string. A daily query never sets these (it becomes a
// Search date string instead), so season/episode are omitempty.
type tvdbQuery struct {
	ID      int    `json:"id"`
	Season  int    `json:"season,omitempty"`
	Episode string `json:"episode,omitempty"`
}

// Search posts an api/torrents query for the search and returns the parsed releases. A
// 401/403 is an auth/rate failure: Prowlarr surfaces HTTP 403 as the query/rate-limit
// being reached, and 401 as bad credentials — both wrap login.ErrLoginFailed so the
// registry records an auth_failure health event (an unauthenticated browse cannot
// distinguish them, and the in-body status 4/5 is the authoritative credential signal).
// A 429/503 is a RateLimitedError; any other non-2xx is an error. The status==0 envelope
// and the status 4/5 -> ErrLoginFailed mapping are handled by parseReleases. Username and
// passkey ride inside the POST body, never the URL, and the body is never logged.
func (d *driver) Search(ctx context.Context, q search.Query) ([]*normalizer.Release, error) {
	body, err := d.buildRequest(q)
	if err != nil {
		return nil, err
	}
	resp, err := d.post(ctx, body)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	switch {
	case resp.StatusCode == stdhttp.StatusUnauthorized || resp.StatusCode == stdhttp.StatusForbidden:
		return nil, fmt.Errorf("hdbits: search unauthorized: %w", login.ErrLoginFailed)
	case search.IsRateLimitStatus(resp.StatusCode):
		return nil, &search.RateLimitedError{
			StatusCode: resp.StatusCode,
			RetryAfter: search.ParseRetryAfter(resp.Header.Get("Retry-After"), d.clock),
		}
	case resp.StatusCode < 200 || resp.StatusCode >= 300:
		return nil, fmt.Errorf("hdbits: search returned HTTP %d", resp.StatusCode)
	}

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		return nil, fmt.Errorf("hdbits: read search response: %w", err)
	}
	return d.parseReleases(respBody)
}

// buildRequest marshals the api/torrents JSON body for a query. The username and passkey
// are read from cfg and placed as top-level fields; the search/category/imdb/tvdb fields
// follow the query, and the limit is the fixed page size. The returned bytes are
// secret-bearing (they embed the credentials) and must never be logged.
func (d *driver) buildRequest(q search.Query) ([]byte, error) {
	tq := torrentQuery{
		Username: strings.TrimSpace(d.cfg["username"]),
		Passkey:  strings.TrimSpace(d.cfg["passkey"]),
		Category: d.categoryParam(q),
		Limit:    searchLimit,
	}
	setSearchCriteria(&tq, q)
	body, err := json.Marshal(tq)
	if err != nil {
		// A marshal error could quote the body (which holds the credentials), so it is
		// scrubbed before it can surface.
		return nil, fmt.Errorf("hdbits: build request body: %s", d.scrubSecrets(err.Error()))
	}
	return body, nil
}

// setSearchCriteria fills the search/imdb/tvdb fields, reproducing Prowlarr's
// HDBitsRequestGenerator: an imdb id sets imdb.id and a verbatim search term; a tvdb id
// sets tvdb.id and either a daily date Search string (when season+episode parse as
// "yyyy MM/dd") or tvdb.season+episode; a plain term is a movie search (sanitized
// [\W]+->' ') when no episode/tvdb signal is present, else a verbatim search term.
func setSearchCriteria(tq *torrentQuery, q search.Query) {
	keywords := strings.TrimSpace(q.Keywords)
	if imdb := imdbID(q.IMDBID); imdb > 0 {
		tq.Imdb = &imdbQuery{ID: imdb}
		tq.Search = keywords
		return
	}
	if tvdb := positiveInt(q.TVDBID); tvdb > 0 {
		setTvdbCriteria(tq, q, tvdb, keywords)
		return
	}
	if keywords == "" {
		return // bare browse
	}
	// A season/episode signal (without an id) is a TV search and uses the term verbatim;
	// a plain keyword is a movie search and is sanitized.
	if positiveInt(q.Season) > 0 || strings.TrimSpace(q.Ep) != "" {
		tq.Search = keywords
		return
	}
	tq.Search = sanitizeMovieTerm(keywords)
}

// setTvdbCriteria fills the tvdb object for a tvdb-id query. A daily episode (season is a
// four-digit year, episode is "MM/dd") drops tvdb.season/episode and sets a "yyyy-MM-dd"
// Search date string (Prowlarr); otherwise tvdb.season/episode carry the standard
// episode. The keyword (if any) rides verbatim as the Search term except in the daily
// case (which owns Search).
func setTvdbCriteria(tq *torrentQuery, q search.Query, tvdb int, keywords string) {
	if daily, ok := dailyDate(q.Season, q.Ep); ok {
		tq.Tvdb = &tvdbQuery{ID: tvdb}
		tq.Search = daily
		return
	}
	tvdbq := &tvdbQuery{ID: tvdb}
	if season := positiveInt(q.Season); season > 0 {
		tvdbq.Season = season
	}
	if ep := strings.TrimSpace(q.Ep); ep != "" {
		tvdbq.Episode = ep
	}
	tq.Tvdb = tvdbq
	tq.Search = keywords
}

// categoryParam maps the resolved tracker categories to the int array HDBits' category
// field expects, de-duplicating while preserving order (Prowlarr sends the distinct
// category ids). q.Categories is already the tracker-id mapping (registry buildQuery); a
// non-numeric id is dropped. An empty result yields nil (omitted from the body).
func (d *driver) categoryParam(q search.Query) []int {
	seen := make(map[int]struct{}, len(q.Categories))
	cats := make([]int, 0, len(q.Categories))
	for _, c := range q.Categories {
		n, err := strconv.Atoi(strings.TrimSpace(c))
		if err != nil || n <= 0 {
			continue
		}
		if _, dup := seen[n]; dup {
			continue
		}
		seen[n] = struct{}{}
		cats = append(cats, n)
	}
	if len(cats) == 0 {
		return nil
	}
	return cats
}

// sanitizeMovieTerm reproduces Prowlarr's movie-path Regex.Replace(term, "[\W]+", " ").
// Trim(): every run of non-word characters collapses to a single space and the result is
// trimmed.
func sanitizeMovieTerm(term string) string {
	return strings.TrimSpace(nonWordRun.ReplaceAllString(term, " "))
}

// imdbID renders an imdb id as the bare numeric Prowlarr submits (ParseUtil.GetImdbId): a
// leading "tt" is stripped and the rest parsed. A non-numeric or empty id yields 0 (no
// imdb search).
func imdbID(raw string) int {
	s := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(raw)), "tt")
	if s == "" {
		return 0
	}
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 {
		return 0
	}
	return n
}

// positiveInt parses raw as a non-negative base-10 int; a blank or unparseable value (or
// a negative) yields 0.
func positiveInt(raw string) int {
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || n < 0 {
		return 0
	}
	return n
}

// dailyDate parses a "{season} {episode}" pair into "yyyy-MM-dd" when season is a
// four-digit year and episode is "MM/dd", matching Prowlarr's DateTime.TryParseExact with
// "yyyy MM/dd" (the HDBits daily search sends an ISO date string). The four-digit-year
// guard keeps Go's lenient year parsing from matching a normal season.
func dailyDate(season, episode string) (string, bool) {
	season = strings.TrimSpace(season)
	episode = strings.TrimSpace(episode)
	if len(season) != 4 {
		return "", false
	}
	t, err := time.Parse("2006 01/02", season+" "+episode)
	if err != nil {
		return "", false
	}
	return t.Format("2006-01-02"), true
}
