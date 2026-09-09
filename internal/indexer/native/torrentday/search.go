package torrentday

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	apphttp "github.com/autobrr/harbrr/internal/http"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/login"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/normalizer"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
	"github.com/autobrr/harbrr/internal/indexer/native"
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
	tokens = append(tokens, q.Categories...)
	if native.CheckboxOn(d.Cfg["freeleech_only"]) {
		tokens = append(tokens, freeleechToken)
	}
	tokens = append(tokens, "q="+url.QueryEscape(d.searchTerm(q)))
	return d.BaseURL + searchPath + "?" + strings.Join(tokens, ";")
}

// searchTerm builds the TorrentDay search term, mirroring Prowlarr's per-criteria
// search string: the keyword, an imdb id when present (no keyword in that case), or the
// keyword plus the SxxExx episode string for a TV query. The result is trimmed.
func (d *driver) searchTerm(q search.Query) string {
	if imdb := native.CanonicalIMDBID(q.IMDBID); imdb != "" {
		return imdb
	}
	keyword := strings.TrimSpace(q.Keywords)
	season := strings.TrimSpace(q.Season)
	ep := strings.TrimSpace(q.Ep)
	if season == "" && ep == "" {
		return keyword
	}
	term := strings.TrimSpace(keyword + " " + q.EpisodeSearchString())
	return strings.TrimSpace(term)
}

// Test verifies the configured session cookie still authenticates (the management
// "test indexer" action) by issuing an empty browse query. A good cookie returns 200 with
// a JSON array; a stale cookie redirects to /login.php (or returns 401/403). Search stamps
// the context WithNoRedirectFollow, so that redirect surfaces as a raw 3xx that
// isLoginRedirect maps to login.ErrLoginFailed (the registry records an auth_failure health
// event) instead of being followed to the login page and misread as a parse error.
func (d *driver) Test(ctx context.Context) error { return native.TestViaSearch(ctx, d) }
