package broadcastthenet

import (
	"context"
	"fmt"
	"io"
	stdhttp "net/http"
	"strconv"
	"strings"
	"time"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/login"
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
	body, err := d.buildRPCBody(d.buildParameters(q), pageResults, pageOffset)
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
		return nil, fmt.Errorf("broadcastthenet: search unauthorized: %w", login.ErrLoginFailed)
	case search.IsRateLimitStatus(resp.StatusCode):
		return nil, &search.RateLimitedError{
			StatusCode: resp.StatusCode,
			RetryAfter: search.ParseRetryAfter(resp.Header.Get("Retry-After"), d.clock),
		}
	case resp.StatusCode < 200 || resp.StatusCode >= 300:
		return nil, fmt.Errorf("broadcastthenet: search returned HTTP %d", resp.StatusCode)
	}

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		return nil, fmt.Errorf("broadcastthenet: read search response: %w", err)
	}
	return d.parseReleases(respBody)
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
// daily query (season is a four-digit year, episode is "MM/dd") -> Name "yyyy.MM.dd%";
// a standard episode (season and episode both >0) -> Name "S{NN}E{EE}%"; a season-only
// query (season>0, no episode) -> Name "S{NN}E%". All carry Category "Episode" except
// the season-only arm, which Prowlarr also requests under "Season"/Name "Season N%" —
// harbrr fetches the single Episode-prefixed page (one request, like FileList).
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
	params.Category = "Episode"
	if episode := positiveInt(q.Ep); episode > 0 {
		params.Name = fmt.Sprintf("S%02dE%02d%%", season, episode)
		return
	}
	params.Name = fmt.Sprintf("S%02dE%%", season)
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
