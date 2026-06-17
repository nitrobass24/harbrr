package filelist

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

// searchPath is the FileList JSON API endpoint (Prowlarr: "{BaseUrl}/api.php").
const searchPath = "api.php"

// Search issues the api.php request for the query and returns the parsed releases.
// A 401/403 is an auth failure wrapped with login.ErrLoginFailed (so the registry
// records an auth_failure health event); a 429 is a rate-limit error; any other
// non-2xx is an error. The Basic header rides on the request (added by get), never
// the URL, so the served (recorded) URL carries no passkey.
func (d *driver) Search(ctx context.Context, q search.Query) ([]*normalizer.Release, error) {
	resp, err := d.get(ctx, d.buildSearchURL(q), "application/json")
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	switch {
	case resp.StatusCode == stdhttp.StatusUnauthorized || resp.StatusCode == stdhttp.StatusForbidden:
		return nil, fmt.Errorf("filelist: search unauthorized: %w", login.ErrLoginFailed)
	case search.IsRateLimitStatus(resp.StatusCode):
		return nil, &search.RateLimitedError{
			StatusCode: resp.StatusCode,
			RetryAfter: search.ParseRetryAfter(resp.Header.Get("Retry-After"), d.clock),
		}
	case resp.StatusCode < 200 || resp.StatusCode >= 300:
		return nil, fmt.Errorf("filelist: search returned HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		return nil, fmt.Errorf("filelist: read search response: %w", err)
	}
	return d.parseReleases(body)
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
	return d.baseURL + searchPath + "?" + params.Encode()
}

// addSearchParams sets action/type/query and (for a name search) season/episode,
// reproducing FileListRequestGenerator.GetSearchRequests. The daily-episode rule is
// Prowlarr's: a "{season} {episode}" pair that parses as "yyyy MM/dd" is a daily show
// — an imdb daily search is skipped (no action set → latest-torrents), and a name
// daily search appends the "yyyy.MM.dd" date to the term instead of sending
// season/episode.
func (d *driver) addSearchParams(params url.Values, q search.Query) {
	imdb := fullIMDBID(q.IMDBID)
	keywords := strings.TrimSpace(sanitizeSearchTerm(q.Keywords))
	if imdb == "" && keywords == "" {
		return // no criteria → latest-torrents (set by addCommonParams)
	}

	if daily, ok := dailyDate(q.Season, q.Ep); ok {
		if imdb != "" {
			return // Prowlarr skips id searches for daily episodes
		}
		params.Set("action", "search-torrents")
		params.Set("type", "name")
		params.Set("query", strings.TrimSpace(keywords+" "+daily))
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
	if cats := distinctCategories(q.Categories); cats != "" {
		params.Set("category", cats)
	}
	if freeleechOnly(d.cfg) {
		params.Set("freeleech", "1")
	}
}

// distinctCategories joins the resolved tracker category ids into the comma-separated
// list Prowlarr sends (string.Join(",", …Distinct())). q.Categories is already the
// tracker-id mapping (registry buildQuery); this only de-duplicates while preserving
// order.
func distinctCategories(cats []string) string {
	seen := make(map[string]struct{}, len(cats))
	distinct := make([]string, 0, len(cats))
	for _, c := range cats {
		c = strings.TrimSpace(c)
		if c == "" {
			continue
		}
		if _, dup := seen[c]; dup {
			continue
		}
		seen[c] = struct{}{}
		distinct = append(distinct, c)
	}
	return strings.Join(distinct, ",")
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

// sanitizeSearchTerm reproduces SearchCriteriaBase.SanitizedSearchTerm: collapse any
// run of Unicode dash punctuation to a single '-', normalize the grave/acute/curly
// single quotes to '\”, then keep only letters, digits, whitespace, and the
// punctuation FileList tolerates (-._()@/'[]+%); every other rune is dropped.
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

// isSafePunct reports whether r is one of the punctuation runes the sanitized search
// term tolerates (the SanitizedSearchTerm whitelist, minus '-' which is handled above).
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
// zero-padded. A non-numeric or empty id yields "" (no imdb search).
func fullIMDBID(raw string) string {
	s := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(raw)), "tt")
	if s == "" {
		return ""
	}
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
