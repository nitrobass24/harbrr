package torrentday

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	apphttp "github.com/autobrr/harbrr/internal/http"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/login"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/normalizer"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
)

const (
	// searchPath is the TorrentDay JSON search endpoint (relative to the base URL).
	searchPath = "t.json"
	// freeleechToken is the path token TorrentDay appends to restrict the search to
	// freeleech torrents (`?<cats>;free;q=term`), mirroring Prowlarr's
	// TorrentDayRequestGenerator.
	freeleechToken = "free"
)

// Search issues the TorrentDay /t.json request for the query and returns the parsed
// releases. The context is stamped WithNoRedirectFollow so a stale-cookie 3xx ->
// /login.php surfaces as a raw redirect (isLoginRedirect) instead of being auto-followed
// to the login page and misread as a parse error; that redirect or a 401/403 is an auth
// failure; a 429/503 is a rate-limit error; any other non-2xx is an error. The cookie
// rides as a header (added by get), never the URL, so the served URL carries no secret.
func (d *driver) Search(ctx context.Context, q search.Query) ([]*normalizer.Release, error) {
	resp, err := d.get(apphttp.WithNoRedirectFollow(ctx), d.buildSearchURL(q), "application/json", false)
	if err != nil {
		if resp != nil && isLoginRedirect(resp) {
			return nil, fmt.Errorf("torrentday: search redirected to login: %w", login.ErrLoginFailed)
		}
		return nil, err
	}
	return d.parseReleases(resp.Body)
}

// buildSearchURL renders the TorrentDay /t.json request, matching Prowlarr's
// TorrentDayRequestGenerator: the resolved tracker category ids are joined with ';'
// directly after '?' (path-style, NOT name=value pairs — e.g. `?29;28`), an optional
// `;free` token when freeleech_only is set, and a trailing `;q=<term>` that is always
// present (the term URL-encoded, empty for a raw browse). harbrr fetches a single page.
//
// The query string is assembled by hand (not url.Values) because TorrentDay's category
// encoding is positional ';'-joined tokens, which url.Values cannot express.
func (d *driver) buildSearchURL(q search.Query) string {
	tokens := make([]string, 0, len(q.Categories)+2)
	tokens = append(tokens, distinct(q.Categories)...)
	if freeleechOnly(d.Cfg) {
		tokens = append(tokens, freeleechToken)
	}
	tokens = append(tokens, "q="+url.QueryEscape(d.searchTerm(q)))
	return d.BaseURL + searchPath + "?" + strings.Join(tokens, ";")
}

// searchTerm builds the TorrentDay search term, mirroring Prowlarr's per-criteria
// search string: the keyword, an imdb id when present (no keyword in that case), or the
// keyword plus the SxxExx episode string for a TV query. The result is trimmed.
func (d *driver) searchTerm(q search.Query) string {
	if imdb := fullIMDBID(q.IMDBID); imdb != "" {
		return imdb
	}
	keyword := strings.TrimSpace(q.Keywords)
	season := strings.TrimSpace(q.Season)
	ep := strings.TrimSpace(q.Ep)
	if season == "" && ep == "" {
		return keyword
	}
	term := strings.TrimSpace(keyword + " " + episodeSearchString(season, ep))
	return strings.TrimSpace(term)
}

// episodeSearchString reproduces the SxxExx rendering: a season with no episode is
// "S{season:00}"; a season+episode is "S{season:00}E{episode:00}"; a seasonless query
// is empty. Non-numeric season/episode values fall back to the raw value.
func episodeSearchString(season, episode string) string {
	if season == "" || season == "0" {
		return ""
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

// distinct returns the input with duplicate tracker categories removed, preserving
// order (Prowlarr's MapTorznabCapsToTrackers(...).Distinct()).
func distinct(cats []string) []string {
	seen := make(map[string]struct{}, len(cats))
	out := make([]string, 0, len(cats))
	for _, c := range cats {
		if _, dup := seen[c]; dup {
			continue
		}
		seen[c] = struct{}{}
		out = append(out, c)
	}
	return out
}
