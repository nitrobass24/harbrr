package broadcastthenet

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/normalizer"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
)

// pageResults is the page size harbrr requests (Prowlarr LimitsDefault/PageSize=100).
// pageOffset is always 0: harbrr fetches one page and paginates response-side
// downstream (a deliberate design choice mirroring FileList, NOT Prowlarr parity, which
// supports server-side paging via the offset param).
const (
	pageResults = 100
	pageOffset  = 0
)

// btnParameters is the getTorrents "parameters" object. Every field is omitempty so an
// unset key is dropped from the JSON (matching Prowlarr's DefaultValueHandling.Ignore),
// and an empty object marshals to {} for a browse/RSS query. Tvdb/Tvrage/Search/Name
// are serialized as strings; Search/Name/Category coexist when both an id/keyword and a
// season/episode are present.
type btnParameters struct {
	Tvdb     string `json:"Tvdb,omitempty"`
	Tvrage   string `json:"Tvrage,omitempty"`
	Search   string `json:"Search,omitempty"`
	Category string `json:"Category,omitempty"`
	Name     string `json:"Name,omitempty"`
}

// Search posts a getTorrents request for the query and returns the parsed releases.
// A 401/403 is an auth failure wrapped with login.ErrLoginFailed (so the registry
// records an auth_failure health event); a rate-limit status is a RateLimitedError; any
// other non-2xx is an error. The in-body JSON-RPC error envelope (e.g. -32001) is
// handled by parseReleases. The API key rides inside the POST body (params[0]), never
// the URL, and the body is never logged.
func (d *driver) Search(ctx context.Context, q search.Query) ([]*normalizer.Release, error) {
	if isAbsoluteEpisodeQuery(q) {
		return []*normalizer.Release{}, nil
	}
	body, err := d.buildRPCBody(d.buildParameters(q), pageResults, pageOffset)
	if err != nil {
		return nil, err
	}
	resp, err := d.post(ctx, body)
	if err != nil {
		return nil, err
	}
	// BTN signals a rate limit as an HTTP 200 body containing "Call Limit Exceeded"
	// (not a 429), so it must be detected before parseReleases would turn it into a
	// parse error — matching Prowlarr's BroadcastheNetParser, which throws
	// RequestLimitReachedException on that body before JSON parsing.
	if isCallLimitBody(resp.Body) {
		return nil, &search.RateLimitedError{
			StatusCode: resp.StatusCode,
			RetryAfter: search.ParseRetryAfter(resp.Header.Get("Retry-After"), d.Clock),
		}
	}
	return d.parseReleases(resp.Body)
}

// callLimitMarker is the substring BTN returns (in an HTTP 200 body) when the API call
// rate limit is hit; Prowlarr matches it case-insensitively before JSON parsing.
const callLimitMarker = "call limit exceeded"

// isCallLimitBody reports whether the response body carries BTN's rate-limit marker
// (case-insensitive).
func isCallLimitBody(body []byte) bool {
	return strings.Contains(strings.ToLower(string(body)), callLimitMarker)
}

// isAbsoluteEpisodeQuery reports whether the query is an absolute-episode lookup that BTN
// cannot serve: a bare non-negative-integer keyword paired with a TVDB or TVRage id and
// no season/episode/daily. Prowlarr's BroadcastheNetRequestGenerator returns an empty
// request chain for this shape (BTN indexes by season/episode Name, not absolute number),
// so harbrr returns zero releases WITHOUT issuing the POST.
func isAbsoluteEpisodeQuery(q search.Query) bool {
	kw := strings.TrimSpace(q.Keywords)
	if kw == "" || !isNonNegativeInteger(kw) {
		return false
	}
	if positiveID(q.TVDBID) == "" && positiveID(q.RageID) == "" {
		return false
	}
	if _, daily := dailyDate(q.Season, q.Ep); daily {
		return false
	}
	return positiveInt(q.Season) == 0 && positiveInt(q.Ep) == 0
}

// isNonNegativeInteger reports whether s is a base-10 non-negative integer.
func isNonNegativeInteger(s string) bool {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	return err == nil && n >= 0
}

// buildParameters maps a query to the getTorrents parameters object, reproducing
// Prowlarr's BroadcastheNetRequestGenerator: Tvdb (when TVDBID>0) else Tvrage (when
// RageID>0); Search with spaces replaced by '%' (set independently of and alongside any
// season/episode); and Category/Name for a season-only, standard-episode, or daily
// query. An empty query yields {} (browse/RSS).
func (d *driver) buildParameters(q search.Query) btnParameters {
	var params btnParameters
	setTvdbOrTvrage(&params, q)
	if kw := strings.TrimSpace(q.Keywords); kw != "" {
		params.Search = strings.ReplaceAll(kw, " ", "%")
	}
	setSeasonEpisode(&params, q)
	return params
}

// setTvdbOrTvrage sets the Tvdb param when a TVDB id is present, else the Tvrage param
// when a TVRage id is present (mutually exclusive, Tvdb wins). BTN has no imdb param.
func setTvdbOrTvrage(params *btnParameters, q search.Query) {
	if tvdb := positiveID(q.TVDBID); tvdb != "" {
		params.Tvdb = tvdb
		return
	}
	if rage := positiveID(q.RageID); rage != "" {
		params.Tvrage = rage
	}
}

// setSeasonEpisode sets Category/Name for the season/episode shape Prowlarr emits: a
// daily query (season is a four-digit year, episode is "MM/dd") -> Category "Episode",
// Name "yyyy.MM.dd%"; a standard episode (season and episode both >0) -> Category
// "Episode", Name "S{NN}E{EE}%"; a season-only query (season>0, no episode) -> Category
// "Season", Name "Season {N}%". BTN files season packs under Category "Season" / Name
// "Season N%" (an "S01E%" Episode query never matches them), so in harbrr's single-page
// model the season-only query emits the SEASON arm — Prowlarr fans both arms out across
// requests, harbrr fetches the single Season-prefixed page (one request, like FileList).
func setSeasonEpisode(params *btnParameters, q search.Query) {
	if daily, ok := dailyDate(q.Season, q.Ep); ok {
		params.Category = "Episode"
		params.Name = daily + "%"
		return
	}
	season := positiveInt(q.Season)
	if season == 0 {
		return
	}
	if episode := positiveInt(q.Ep); episode > 0 {
		params.Category = "Episode"
		params.Name = fmt.Sprintf("S%02dE%02d%%", season, episode)
		return
	}
	params.Category = "Season"
	params.Name = fmt.Sprintf("Season %d%%", season)
}

// dailyDate parses a "{season} {episode}" pair into "yyyy.MM.dd" when season is a
// four-digit year and episode is "MM/dd", matching Prowlarr's DateTime.TryParseExact
// with "yyyy MM/dd". The four-digit-year guard keeps Go's lenient year parsing from
// matching a normal season.
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
	return t.Format("2006.01.02"), true
}

// positiveID renders an id string as itself when it parses to a positive integer, else
// "" (BTN sends Tvdb/Tvrage only when the id is > 0).
func positiveID(raw string) string {
	if n := positiveInt(raw); n > 0 {
		return strconv.Itoa(n)
	}
	return ""
}

// positiveInt parses raw as a non-negative base-10 int; a blank or unparseable value
// (or a negative) yields 0.
func positiveInt(raw string) int {
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || n < 0 {
		return 0
	}
	return n
}
