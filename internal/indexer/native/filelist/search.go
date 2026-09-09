package filelist

import (
	"context"
	"net/url"
	"strings"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/normalizer"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
	"github.com/autobrr/harbrr/internal/indexer/native"
)

// searchPath is the FileList JSON API endpoint (Prowlarr: "{BaseUrl}/api.php").
const searchPath = "api.php"

// Search issues the api.php request for the query and returns the parsed releases.
// A 401/403 is an auth failure wrapped with login.ErrLoginFailed (so the registry
// records an auth_failure health event); a 429 is a rate-limit error; any other
// non-2xx is an error. The Basic header rides on the request (added by get), never
// the URL, so the served (recorded) URL carries no passkey.
func (d *driver) Search(ctx context.Context, q search.Query) ([]*normalizer.Release, error) {
	resp, err := d.get(ctx, d.buildSearchURL(q), "application/json", false)
	if err != nil {
		return nil, err
	}
	return d.parseReleases(resp.Body)
}

// buildSearchURL renders the api.php request for a query, matching Prowlarr's
// FileListRequestGenerator: action=search-torrents when an imdb id or keyword is
// present (else latest-torrents — the cheap Test() probe), type=imdb with the full
// imdb id when present else type=name with the sanitized term, season/episode when
// present, the distinct tracker categories as a csv, and freeleech=1 when the setting
// is on. There is no pagination (Prowlarr yields a single request). The passkey rides
// as the Basic header (added by get), never the URL, so the URL carries no secret.
func (d *driver) buildSearchURL(q search.Query) string {
	params := url.Values{}
	d.addSearchParams(params, q)
	d.addCommonParams(params, q)
	return d.BaseURL + searchPath + "?" + params.Encode()
}

// addSearchParams sets action/type/query and (for a name search) season/episode,
// reproducing FileListRequestGenerator.GetSearchRequests. The daily-episode rule is
// Prowlarr's: a "{season} {episode}" pair that parses as "yyyy MM/dd" is a daily show
// — an imdb daily search is skipped (no action set → latest-torrents), and a name
// daily search appends the "yyyy.MM.dd" date to the term instead of sending
// season/episode.
func (d *driver) addSearchParams(params url.Values, q search.Query) {
	imdb := native.CanonicalIMDBID(q.IMDBID)
	keywords := strings.TrimSpace(native.SanitizeSearchTerm(q.Keywords))
	if imdb == "" && keywords == "" {
		return // no criteria → latest-torrents (set by addCommonParams)
	}

	if daily, ok := native.DailyEpisodeDate(q.Season, q.Ep); ok {
		if imdb != "" {
			return // Prowlarr skips id searches for daily episodes
		}
		params.Set("action", "search-torrents")
		params.Set("type", "name")
		params.Set("query", strings.TrimSpace(keywords+" "+daily.Format("2006.01.02")))
		return
	}

	params.Set("action", "search-torrents")
	if season := strings.TrimSpace(q.Season); season != "" && season != "0" {
		params.Set("season", season)
	}
	if ep := strings.TrimSpace(q.Ep); ep != "" {
		params.Set("episode", ep)
	}
	if imdb != "" {
		params.Set("type", "imdb")
		params.Set("query", imdb)
		return
	}
	params.Set("type", "name")
	params.Set("query", keywords)
}

// addCommonParams reproduces FileListRequestGenerator.GetPagedRequests: action
// defaults to latest-torrents when no search criteria set it, the distinct tracker
// categories are joined as a csv, and freeleech=1 is added when the setting is on.
func (d *driver) addCommonParams(params url.Values, q search.Query) {
	if params.Get("action") == "" {
		params.Set("action", "latest-torrents")
	}
	if cats := strings.Join(q.Categories, ","); cats != "" {
		params.Set("category", cats)
	}
	if native.CheckboxOn(d.Cfg["freeleech_only"]) {
		params.Set("freeleech", "1")
	}
}
