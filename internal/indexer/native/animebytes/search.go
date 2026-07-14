package animebytes

import (
	"context"
	"errors"
	stdhttp "net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/mapper"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/normalizer"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
)

// searchPath is the AnimeBytes scrape endpoint (Prowlarr: "{BaseUrl}/scrape.php").
const searchPath = "scrape.php"

// searchType is the AnimeBytes top-level search namespace. Prowlarr dispatches music
// searches to "music" and everything else (anime/tv/movie/book/basic) to "anime".
const (
	searchTypeAnime = "anime"
	searchTypeMusic = "music"
)

// Search issues the scrape.php request for the query and returns the parsed releases.
// The full URL carries the username + passkey, so it is never logged (get redacts it on
// a transport error). A 401/403 is an auth failure (login.ErrLoginFailed); a 429/503 is
// a rate-limit error; any other non-2xx is an error. On 2xx the body is parsed by
// parseReleases, which also discriminates the JSON {"error":…} envelope AnimeBytes
// returns with HTTP 200.
func (d *driver) Search(ctx context.Context, q search.Query) ([]*normalizer.Release, error) {
	resp, err := d.get(ctx, d.buildSearchURL(q), "application/json", false)
	if err != nil {
		if resp != nil && resp.StatusCode != stdhttp.StatusUnauthorized && resp.StatusCode != stdhttp.StatusForbidden {
			var rl *search.RateLimitedError
			if !errors.As(err, &rl) {
				return nil, search.ErrParseError
			}
		}
		return nil, err
	}
	return d.parseReleases(resp.Body)
}

// buildSearchURL renders the scrape.php request for a query, reproducing Prowlarr's
// AnimeBytesRequestGenerator.GetRequest: username + torrent_pass auth in the query,
// sort=grouptime/way=desc, type=anime|music, the cleaned searchstr, limit=50 when a term
// is present (else 20), the music artist/album/year params, the distinct tracker
// categories each as a "key=1" flag (Prowlarr's parameters.Set(cat,"1")), and freeleech=1
// when the setting is on. The returned URL is secret-bearing (username + passkey) and is
// never logged.
func (d *driver) buildSearchURL(q search.Query) string {
	params := url.Values{}
	params.Set("username", strings.TrimSpace(d.Cfg["username"]))
	params.Set("torrent_pass", strings.TrimSpace(d.Cfg["passkey"]))
	params.Set("sort", "grouptime")
	params.Set("way", "desc")

	typ := searchTypeFor(q)
	params.Set("type", typ)

	term := cleanSearchTerm(strings.TrimSpace(q.Keywords))
	params.Set("searchstr", term)
	params.Set("limit", searchLimit(term))

	d.addTypeParams(params, q, typ)
	d.addCategoryParams(params, q)
	if freeleechOnly(d.Cfg) {
		params.Set("freeleech", "1")
	}
	return d.BaseURL + searchPath + "?" + params.Encode()
}

// addTypeParams sets the music-only artist/album/year params, matching Prowlarr's
// MusicSearchCriteria branch (artistnames, groupname, year). For an anime search nothing
// extra is added here (the optional SearchByYear flag is a deferred parity feature).
func (d *driver) addTypeParams(params url.Values, q search.Query, typ string) {
	if typ != searchTypeMusic {
		return
	}
	if artist := strings.TrimSpace(q.Artist); artist != "" && artist != "VA" {
		params.Set("artistnames", artist)
	}
	if album := strings.TrimSpace(q.Album); album != "" {
		params.Set("groupname", album)
	}
	if year := strings.TrimSpace(q.Year); year != "" && year != "0" {
		params.Set("year", year)
	}
}

// addCategoryParams sets each distinct resolved tracker category id as a "key=1" flag,
// reproducing Prowlarr's queryCats.ForEach(cat => parameters.Set(cat, "1")). q.Categories
// is already the tracker-id mapping (registry buildQuery); this only de-duplicates while
// preserving order.
func (d *driver) addCategoryParams(params url.Values, q search.Query) {
	seen := make(map[string]struct{}, len(q.Categories))
	for _, c := range q.Categories {
		c = strings.TrimSpace(c)
		if c == "" {
			continue
		}
		if _, dup := seen[c]; dup {
			continue
		}
		seen[c] = struct{}{}
		params.Set(c, "1")
	}
}

// searchTypeFor picks the AnimeBytes top-level type. A music-search query — signalled
// either by the Torznab mode (t=music, so q.Mode == music-search) or by music params
// (artist/album) — routes to the music corpus; everything else is an anime search
// (Prowlarr maps movie/tv/book/basic all to "anime"). The mode signal is what lets a
// keyword-only music query (no artist/album) reach the music namespace.
func searchTypeFor(q search.Query) string {
	if q.Mode == mapper.ModeMusicSearch ||
		strings.TrimSpace(q.Artist) != "" || strings.TrimSpace(q.Album) != "" {
		return searchTypeMusic
	}
	return searchTypeAnime
}

// searchLimit is Prowlarr's page size: 50 when a search term is present, 20 for the
// empty (latest/Test) probe.
func searchLimit(term string) string {
	if term != "" {
		return "50"
	}
	return "20"
}

// trailingEpisodeRe / trailingSeasonEpRe / trailingNumberRe reproduce Prowlarr's
// CleanSearchTerm regexes: AnimeBytes' tracer cannot search by episode number, so a
// trailing "x05" / "5x05", a trailing "S01E05", and a trailing bare number are stripped.
var (
	trailingEpisodeRe  = regexp.MustCompile(`\W(\dx)?\d?\d$`)
	trailingSeasonEpRe = regexp.MustCompile(`\W(S\d\d?E)?\d?\d$`)
	trailingNumberRe   = regexp.MustCompile(`\W\d+$`)
	trailingTheMovieRe = regexp.MustCompile(`(?i)\bThe Movie$`)
)

// cleanSearchTerm reproduces Prowlarr's AnimeBytes CleanSearchTerm: strip a trailing
// episode/season-episode/number token (the tracer cannot search by episode), then drop a
// trailing "The Movie". The input is assumed already trimmed; the result is re-trimmed.
func cleanSearchTerm(term string) string {
	term = trailingEpisodeRe.ReplaceAllString(term, "")
	term = trailingSeasonEpRe.ReplaceAllString(term, "")
	term = trailingNumberRe.ReplaceAllString(term, "")
	term = trailingTheMovieRe.ReplaceAllString(strings.TrimSpace(term), "")
	return strings.TrimSpace(term)
}
