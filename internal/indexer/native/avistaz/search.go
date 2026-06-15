package avistaz

import (
	"context"
	"fmt"
	"io"
	stdhttp "net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/login"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/normalizer"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
)

const (
	searchPath = "api/v1/jackett/torrents"
	// pageSize is the Avistaz API PageSize (Prowlarr/Jackett); it is the per-request
	// `limit`. harbrr pages the served feed itself, so every search fetches one page.
	pageSize = 50
)

// searchKind selects the per-search-type parameter mapping. harbrr's search.Query
// drops the Torznab `t=` function (movie/tvsearch/search), so the kind is inferred
// from the resolved tracker categories (1=Movies, 2=TV) plus the id/episode fields —
// which reproduces Prowlarr's per-criteria request bytes (see classify).
type searchKind int

const (
	kindBasic searchKind = iota
	kindMovie
	kindTV
)

// Search issues the api/v1/jackett/torrents request for the query and returns the
// parsed releases. A 404 is "no results" (not an error, matching Prowlarr's
// suppression of NotFound); a 429 is a rate-limit error; any other non-2xx is an
// error. The response body is parsed in the parser commit.
func (d *driver) Search(ctx context.Context, q search.Query) ([]*normalizer.Release, error) {
	resp, err := d.get(ctx, d.buildSearchURL(q), "application/json")
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	switch {
	case resp.StatusCode == stdhttp.StatusNotFound:
		return nil, nil
	case search.IsRateLimitStatus(resp.StatusCode):
		return nil, &search.RateLimitedError{
			StatusCode: resp.StatusCode,
			RetryAfter: search.ParseRetryAfter(resp.Header.Get("Retry-After"), d.clock),
		}
	case resp.StatusCode == stdhttp.StatusUnauthorized || resp.StatusCode == stdhttp.StatusPreconditionFailed:
		// A 401/412 that survives get's reactive re-auth is a genuine auth failure.
		return nil, fmt.Errorf("avistaz: search unauthorized: %w", login.ErrLoginFailed)
	case resp.StatusCode < 200 || resp.StatusCode >= 300:
		return nil, fmt.Errorf("avistaz: search returned HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		return nil, fmt.Errorf("avistaz: read search response: %w", err)
	}
	return d.parseReleases(body)
}

// buildSearchURL renders the api/v1/jackett/torrents request for a query, matching
// Prowlarr's AvistazRequestGenerator: the constant in=1, the single `type` derived
// from the categories, limit=PageSize, an optional discount[] for freeleech, and the
// per-kind id/search params. The bearer rides as a header (added by get), never the
// URL, so the URL carries no secret.
//
// Two Prowlarr params are deliberately omitted: `tags` (genre) — harbrr's search.Query
// has no genre field; and `video_quality[]` — harbrr collapses the requested
// resolution categories to the tracker id (1/2) before the driver runs, so the
// resolution is unavailable here. Both are compensated response-side: the served feed
// is narrowed to the requested resolution by the torznab category filter. See the
// testdata README divergence note.
func (d *driver) buildSearchURL(q search.Query) string {
	typ := derivedType(q.Categories)
	params := url.Values{}
	params.Set("in", "1")
	params.Set("type", typ)
	params.Set("limit", strconv.Itoa(pageSize))
	if freeleechOnly(d.cfg) {
		params.Add("discount[]", "1")
	}
	d.addQueryParams(params, q, d.classify(q, typ))
	return d.baseURL + searchPath + "?" + params.Encode()
}

// addQueryParams adds the id/search params for the kind, mirroring the precedence in
// AvistazRequestGenerator.GetSearchRequests: movie is id-preferred (imdb else tmdb
// else search, no search on an id query); tv is id-preferred but always carries the
// episode term as `search`; basic/exotica is search-only.
func (d *driver) addQueryParams(params url.Values, q search.Query, kind searchKind) {
	switch kind {
	case kindMovie:
		switch {
		case q.IMDBID != "":
			params.Set("imdb", fullIMDBID(q.IMDBID))
		case q.TMDBID != "":
			params.Set("tmdb", strings.TrimSpace(q.TMDBID))
		default:
			params.Set("search", strings.TrimSpace(sanitizeSearchTerm(q.Keywords)))
		}
	case kindTV:
		ep := d.episodeSearchTerm(q)
		switch {
		case q.IMDBID != "":
			params.Set("imdb", fullIMDBID(q.IMDBID))
			params.Set("search", strings.TrimSpace(ep))
		case q.TVDBID != "":
			params.Set("tvdb", strings.TrimSpace(q.TVDBID))
			params.Set("search", strings.TrimSpace(ep))
		default:
			params.Set("search", strings.TrimSpace(sanitizeSearchTerm(q.Keywords)+" "+ep))
		}
	case kindBasic:
		params.Set("search", strings.TrimSpace(sanitizeSearchTerm(q.Keywords)))
	}
}

// classify infers the search kind from the resolved categories and query fields,
// reproducing the request bytes Prowlarr emits per search criteria even though harbrr
// does not carry the Torznab `t=` function. A TV category or any season/episode/tvdb
// signal => TV; a Movie category or a tmdb/imdb id => Movie; otherwise basic. The
// inference is byte-equivalent to Prowlarr in the cases that matter because a movie
// and a tv request differ only by the episode term, which is empty without a
// season/episode. ExoticaZ is always basic (it advertises no movie/tv params).
func (d *driver) classify(q search.Query, typ string) searchKind {
	switch {
	case d.profile.exoticaParse:
		return kindBasic
	case typ == "2" || q.Season != "" || q.Ep != "" || q.TVDBID != "":
		return kindTV
	case typ == "1" || q.TMDBID != "" || q.IMDBID != "":
		return kindMovie
	default:
		return kindBasic
	}
}

// derivedType reproduces Prowlarr's categoryMapping.FirstIfSingleOrDefault("0"): the
// single distinct tracker category id when exactly one was requested, else "0" (no
// category, or a mix the API cannot express as a single type). q.Categories is already
// the distinct tracker-id mapping (registry buildQuery), so this only collapses it to
// a single value or the default.
func derivedType(cats []string) string {
	seen := make(map[string]struct{}, len(cats))
	distinct := make([]string, 0, len(cats))
	for _, c := range cats {
		if _, dup := seen[c]; dup {
			continue
		}
		seen[c] = struct{}{}
		distinct = append(distinct, c)
	}
	if len(distinct) == 1 {
		return distinct[0]
	}
	return "0"
}

// episodeSearchTerm is Prowlarr's GetEpisodeSearchTerm: the base EpisodeSearchString,
// except AvistaZ (episodeOverride) renders a seasonless episode as "E{episode}" (e.g.
// "Running Man E323") rather than the empty string the base produces.
func (d *driver) episodeSearchTerm(q search.Query) string {
	season := strings.TrimSpace(q.Season)
	ep := strings.TrimSpace(q.Ep)
	if d.profile.episodeOverride && (season == "" || season == "0") && ep != "" {
		return "E" + ep
	}
	return episodeSearchString(season, ep)
}

// episodeSearchString reproduces TvSearchCriteria.EpisodeSearchString: a seasonless
// query is empty; a "{year} {MM/dd}" pair is a daily date "yyyy.MM.dd"; a season with
// no episode is "S{season:00}"; otherwise "S{season:00}E{episode:00}" (the episode
// coerced to an int, falling back to the raw episode when it is not numeric).
func episodeSearchString(season, episode string) string {
	if season == "" || season == "0" {
		return ""
	}
	if daily, ok := dailyDate(season, episode); ok {
		return daily
	}
	seasonPart := season
	if n, err := strconv.Atoi(season); err == nil {
		seasonPart = fmt.Sprintf("%02d", n)
	}
	if episode == "" {
		return "S" + seasonPart
	}
	if n, err := strconv.Atoi(episode); err == nil {
		return fmt.Sprintf("S%sE%02d", seasonPart, n)
	}
	return "S" + seasonPart + "E" + episode
}

// dailyDate parses a "{year} {MM/dd}" season/episode pair (a daily show) into
// "yyyy.MM.dd", matching the DateTime.TryParseExact in EpisodeSearchString. The
// four-digit-year guard keeps Go's lenient year parsing from matching a normal season.
func dailyDate(season, episode string) (string, bool) {
	if len(season) != 4 {
		return "", false
	}
	t, err := time.Parse("2006 01/02", season+" "+episode)
	if err != nil {
		return "", false
	}
	return t.Format("2006.01.02"), true
}

// sanitizeSearchTerm reproduces SearchCriteriaBase.SanitizedSearchTerm: collapse any
// run of Unicode dash punctuation to a single '-', normalize the grave/acute/curly
// single quotes to '\”, then keep only letters, digits, whitespace, and the
// punctuation the Avistaz API tolerates (-._()@/'[]+%); every other rune is dropped.
func sanitizeSearchTerm(term string) string {
	var b strings.Builder
	b.Grow(len(term))
	prevDash := false
	for _, r := range term {
		if unicode.Is(unicode.Pd, r) { // any dash punctuation -> a single '-'
			if !prevDash {
				b.WriteByte('-')
				prevDash = true
			}
			continue
		}
		prevDash = false
		switch {
		case r == '`', r == '´', r == '‘', r == '’':
			b.WriteByte('\'')
		case unicode.IsLetter(r), unicode.IsDigit(r), unicode.IsSpace(r), isSafePunct(r):
			b.WriteRune(r)
		}
	}
	return b.String()
}

// isSafePunct reports whether r is one of the punctuation runes Avistaz's search term
// tolerates (the SanitizedSearchTerm whitelist, minus '-' which is handled above).
func isSafePunct(r rune) bool {
	switch r {
	case '.', '_', '(', ')', '@', '/', '\'', '[', ']', '+', '%':
		return true
	default:
		return false
	}
}

// fullIMDBID renders an imdb id as Prowlarr's FullImdbId ("tt" + the numeric id, a
// minimum of seven digits): a leading "tt" is stripped, the rest parsed and
// zero-padded. A non-numeric id yields "" (the param is then sent empty, as Prowlarr
// would render a null FullImdbId).
func fullIMDBID(raw string) string {
	s := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(raw)), "tt")
	n, err := strconv.Atoi(s)
	if err != nil {
		return ""
	}
	return fmt.Sprintf("tt%07d", n)
}

// freeleechOnly reports whether the freeleech_only checkbox is enabled. harbrr stores
// a checked checkbox as Jackett's "True" sentinel; common truthy spellings are also
// accepted so whatever the management API persists is interpreted consistently.
func freeleechOnly(cfg map[string]string) bool {
	switch strings.ToLower(strings.TrimSpace(cfg["freeleech_only"])) {
	case "true", "1", "on", "yes":
		return true
	default:
		return false
	}
}
