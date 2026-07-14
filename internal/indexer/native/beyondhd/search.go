package beyondhd

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/normalizer"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
)

// searchAction is the only action the driver issues; BeyondHD's api/torrents body always
// carries "action":"search" (Prowlarr BeyondHDRequestGenerator).
const searchAction = "search"

// bhdRequest is the api/torrents JSON POST body (Prowlarr's BeyondHDRequest). Action and
// rsskey are always present (rsskey is a required body credential on EVERY search, so the
// ENTIRE marshalled body is secret-bearing and never logged). Every other field is
// omitempty so an unset key is dropped: a bare browse/RSS query marshals to just the action
// and the rsskey. imdb_id and tmdb_id are mutually exclusive (imdb wins), set by the
// builder. Categories is an int array (the tracker category ids), omitted when empty.
type bhdRequest struct {
	Action     string `json:"action"`
	RSSKey     string `json:"rsskey"`
	Search     string `json:"search,omitempty"`
	Categories []int  `json:"categories,omitempty"`
	ImdbID     string `json:"imdb_id,omitempty"`
	TmdbID     string `json:"tmdb_id,omitempty"`
}

// Search posts an api/torrents query for the search and returns the parsed releases. A
// 401/403 is bad credentials (login.ErrLoginFailed -> auth_failure health); a rate-limit
// status (429/503) is a RateLimitedError so the registry backs off; any other non-2xx is an
// error. The status_code==0 envelope and the "Invalid API Key" body marker are handled by
// parseReleases. The api_key rides in the URL path and the rsskey rides inside the POST
// body — neither the URL nor the body is ever logged.
func (d *driver) Search(ctx context.Context, q search.Query) ([]*normalizer.Release, error) {
	body, err := d.buildRequest(q)
	if err != nil {
		return nil, err
	}
	resp, err := d.post(ctx, body)
	if err != nil {
		return nil, err
	}
	return d.parseReleases(resp.Body)
}

// buildRequest marshals the api/torrents JSON body for a query. The rsskey is read from cfg
// and placed as a top-level field; the search/categories/imdb_id/tmdb_id fields follow the
// query. The returned bytes are secret-bearing (they embed the rsskey) and must never be
// logged.
func (d *driver) buildRequest(q search.Query) ([]byte, error) {
	req := bhdRequest{
		Action:     searchAction,
		RSSKey:     strings.TrimSpace(d.Cfg["rsskey"]),
		Categories: d.categoryParam(q),
	}
	setSearchCriteria(&req, q)
	body, err := json.Marshal(req)
	if err != nil {
		// A marshal error could quote the body (which holds the rsskey), so it is scrubbed
		// before it can surface.
		return nil, fmt.Errorf("beyondhd: build request body: %s", d.scrubSecrets(err.Error()))
	}
	return body, nil
}

// setSearchCriteria fills the search/imdb_id/tmdb_id fields, reproducing Prowlarr's
// BeyondHDRequestGenerator: a full tt-prefixed imdb id sets imdb_id (and wins over tmdb);
// otherwise a tmdb id sets tmdb_id as the "movie/<id>" string. Regardless of the id branch,
// the search term carries the keyword PLUS any TV season/episode qualifier (so an imdb/tmdb
// TV search keeps its SxxExx and does not broaden to the whole series); a plain keyword is a
// verbatim search term. imdb_id and tmdb_id are mutually exclusive.
func setSearchCriteria(req *bhdRequest, q search.Query) {
	if imdb := imdbID(q.IMDBID); imdb != "" {
		req.ImdbID = imdb
		req.Search = tvSearchTerm(q)
		return
	}
	if tmdb := tmdbParam(q.TMDBID); tmdb != "" {
		req.TmdbID = tmdb
		req.Search = tvSearchTerm(q)
		return
	}
	req.Search = tvSearchTerm(q)
}

// tvSearchTerm builds the keyword for a (possibly TV) query: a season/episode signal
// appends the formatted episode component (a daily episode becomes " yyyy-MM-dd", a
// season+episode " SxxExx", a season alone " Sxx") to the trimmed keyword; a plain query is
// the trimmed keyword. An empty result (bare browse/RSS) drops the search field.
func tvSearchTerm(q search.Query) string {
	keywords := strings.TrimSpace(q.Keywords)
	suffix := episodeSearchString(q.Season, q.Ep)
	if suffix == "" {
		return keywords
	}
	return strings.TrimSpace(keywords + " " + suffix)
}

// categoryParam maps the resolved tracker categories to the int array the categories body
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

// imdbID renders an imdb id as the FULL tt-prefixed form BeyondHD's imdb_id expects
// (Prowlarr submits FullImdbId, "tt" + the numeric zero-padded to 7 digits, e.g.
// "tt0133093"): both a bare numeric id and a tt-prefixed id normalise to the same padded
// form. A blank or non-numeric id yields "" (no imdb search).
func imdbID(raw string) string {
	s := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(raw)), "tt")
	if s == "" {
		return ""
	}
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 {
		return ""
	}
	return fmt.Sprintf("tt%07d", n)
}

// tmdbParam renders a tmdb id as the "movie/<id>" string BeyondHD's tmdb_id expects
// (Prowlarr's TmdbId form). A blank or non-numeric id yields "" (no tmdb search).
func tmdbParam(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ""
	}
	if n, err := strconv.Atoi(s); err != nil || n <= 0 {
		return ""
	}
	return "movie/" + s
}

// positiveInt parses raw as a non-negative base-10 int; a blank or unparseable value (or a
// negative) yields 0.
func positiveInt(raw string) int {
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || n < 0 {
		return 0
	}
	return n
}

// episodeSearchString formats the season/episode component appended to a TV search term: a
// daily episode (season a four-digit year, episode "MM/dd") becomes "yyyy-MM-dd" (Prowlarr
// rewrites the term to "<term> yyyy-MM-dd" when the episode parses as a date); a
// season+episode becomes "S%02dE%02d"; a season alone becomes "S%02d"; anything else is
// empty.
func episodeSearchString(season, ep string) string {
	if daily, ok := dailyDate(season, ep); ok {
		return daily
	}
	s := positiveInt(season)
	if s <= 0 {
		return ""
	}
	if e := positiveInt(ep); strings.TrimSpace(ep) != "" && e > 0 {
		return fmt.Sprintf("S%02dE%02d", s, e)
	}
	return fmt.Sprintf("S%02d", s)
}

// dailyDate parses a "{season} {episode}" pair into "yyyy-MM-dd" when season is a
// four-digit year and episode is "MM/dd", matching Prowlarr's DateTime.TryParseExact with
// "yyyy MM/dd". The four-digit-year guard keeps Go's lenient year parsing from matching a
// normal season.
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
