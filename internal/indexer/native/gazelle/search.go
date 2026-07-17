package gazelle

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/normalizer"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
)

// vaArtist is the "various artists" sentinel Prowlarr skips for the artistname param —
// a VA compilation has no single artist to filter on.
const vaArtist = "VA"

// Search issues the authenticated browse request for the query and returns the parsed
// releases. A site with no upstream paging (pageSize == 0: RED/OPS) fetches a single
// page; a fixed-page site (AlphaRatio) pages its API and renews its session once via
// sessionRetry after a redirect, 401/403, or auth-failure body. Credentials ride only in
// request headers/cookies and are never logged.
func (d *driver) Search(ctx context.Context, q search.Query) ([]*normalizer.Release, error) {
	if d.site.pageSize > 0 {
		return d.searchPaged(ctx, q)
	}
	return d.searchPage(ctx, d.buildBrowseURL(q))
}

// searchPage fetches and parses one browse page through sessionRetry: the site's
// strategy attaches auth on every attempt (newRequest), and an auth-classified failure
// — from the transport status or from parseBrowse's body inspection — gets exactly one
// recovery-and-retry via the strategy before surfacing.
func (d *driver) searchPage(ctx context.Context, rawURL string) ([]*normalizer.Release, error) {
	return sessionRetry(ctx, d, "search", func(ctx context.Context) ([]*normalizer.Release, error) {
		req, err := d.newRequest(ctx, rawURL)
		if err != nil {
			return nil, err
		}
		// Snapshot the session AFTER Prepare (so it reflects what actually rode on
		// req) but BEFORE Do, so a concurrent renewal mid-flight can't be confused
		// with the cookie this specific request used.
		requestCookie := d.sessionSnapshot().cookie
		resp, err := d.Do(d.requestContext(ctx), req, d.site.classify)
		if err != nil {
			return nil, err
		}
		return d.parseBrowse(resp.Body, requestCookie)
	})
}

// searchPaged translates harbrr's arbitrary offset/limit window to a fixed-page
// upstream API. It fetches only the pages overlapping the requested window, then trims
// the leading partial page and requested limit before the shared Torznab paging layer
// sees the result.
func (d *driver) searchPaged(ctx context.Context, q search.Query) ([]*normalizer.Release, error) {
	pageSize := d.site.pageSize
	limit := q.Limit
	if limit <= 0 {
		limit = pageSize
	}
	offset := max(q.Offset, 0)
	firstPage := offset/pageSize + 1
	leading := offset % pageSize
	pagesNeeded := (leading + limit + pageSize - 1) / pageSize

	releases := make([]*normalizer.Release, 0, pagesNeeded*pageSize)
	for page := firstPage; page < firstPage+pagesNeeded; page++ {
		pageReleases, err := d.searchPage(ctx, d.buildBrowseURL(q, page))
		if err != nil {
			return nil, err
		}
		releases = append(releases, pageReleases...)
		if len(pageReleases) < pageSize {
			break
		}
	}
	if leading >= len(releases) {
		return nil, nil
	}
	releases = releases[leading:]
	if len(releases) > limit {
		releases = releases[:limit]
	}
	return releases, nil
}

// buildBrowseURL composes the ajax.php?action=browse request URL. order_by=time and
// order_way=desc are always set; searchstr carries the free-text term (dots replaced by
// spaces, per Prowlarr); the fielded music params artistname/groupname/year are set when
// present (artistname is skipped for an empty value or "VA"); and one filter_cat[<id>]=1
// is emitted per requested tracker category. recordlabel is intentionally NOT sent —
// RED/OPS do not advertise or use a Label param (Prowlarr RED/OPS RequestGenerator). A
// site's buildQuery hook (AlphaRatio's freeleech/scene/IMDB/page params) adds anything
// beyond this shared set. The URL carries no secret.
func (d *driver) buildBrowseURL(q search.Query, requestedPage ...int) string {
	page := 0
	if len(requestedPage) > 0 {
		page = requestedPage[0]
	}
	params := url.Values{}
	params.Set("action", "browse")
	params.Set("order_by", "time")
	params.Set("order_way", "desc")
	if term := sanitizeTerm(q.Keywords); term != "" {
		params.Set("searchstr", term)
	}
	if artist := strings.TrimSpace(q.Artist); artist != "" && artist != vaArtist {
		params.Set("artistname", artist)
	}
	if album := strings.TrimSpace(q.Album); album != "" {
		params.Set("groupname", album)
	}
	if year := strings.TrimSpace(q.Year); year != "" {
		params.Set("year", year)
	}
	if d.site.buildQuery != nil {
		d.site.buildQuery(d, q, page, params)
	}
	encoded := params.Encode()
	if cats := filterCats(q.Categories); cats != "" {
		encoded += "&" + cats
	}
	return fmt.Sprintf("%sajax.php?%s", d.BaseURL, encoded)
}

func truthy(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "true", "1", "on", "yes":
		return true
	default:
		return false
	}
}

// sanitizeTerm trims the free-text term and replaces dots with spaces, matching
// Prowlarr's GazelleRequestGenerator term handling (Trim + Replace(".", " ")).
func sanitizeTerm(keywords string) string {
	return strings.ReplaceAll(strings.TrimSpace(keywords), ".", " ")
}

// fullIMDBID renders an imdb id as Jackett's GetFullImdbId ("tt" + the numeric id, a
// minimum of seven digits): a leading "tt" is stripped, the rest parsed and
// zero-padded. A non-numeric id yields "" (the taglist param is then omitted).
func fullIMDBID(raw string) string {
	s := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(raw)), "tt")
	n, err := strconv.Atoi(s)
	if err != nil {
		return ""
	}
	return fmt.Sprintf("tt%07d", n)
}

// filterCats renders the per-category filter_cat[<id>]=1 params Prowlarr emits, one per
// requested tracker category, de-duplicated in request order. q.Categories already holds
// the tracker category ids (the Torznab layer mapped the newznab cats to tracker cats
// before building the query), so each id is emitted verbatim. The "[" / "]" in the key
// are percent-encoded so the URL is well-formed.
func filterCats(cats []string) string {
	seen := make(map[string]struct{}, len(cats))
	parts := make([]string, 0, len(cats))
	for _, c := range cats {
		c = strings.TrimSpace(c)
		if c == "" {
			continue
		}
		if _, dup := seen[c]; dup {
			continue
		}
		seen[c] = struct{}{}
		parts = append(parts, url.QueryEscape(fmt.Sprintf("filter_cat[%s]", c))+"=1")
	}
	return strings.Join(parts, "&")
}
