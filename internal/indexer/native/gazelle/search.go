package gazelle

import (
	"context"
	"fmt"
	"io"
	stdhttp "net/http"
	"net/url"
	"strings"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/login"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/normalizer"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
)

// maxBodyBytes caps a browse response. A Gazelle browse page is small JSON (LimitsMax
// = 50 groups, each with a handful of nested torrents), so this is generous while still
// bounding a hostile or runaway body.
const maxBodyBytes = 8 << 20 // 8 MiB

// vaArtist is the "various artists" sentinel Prowlarr skips for the artistname param —
// a VA compilation has no single artist to filter on.
const vaArtist = "VA"

// Search issues the authenticated browse request for the query and returns the parsed
// releases. A 401/403 is an auth failure wrapped with login.ErrLoginFailed (so the
// registry records an auth_failure health event); a rate-limit status is a
// RateLimitedError carrying any Retry-After; any other non-2xx is an error. A 200 body
// is handed to parseBrowse, which distinguishes a status:"failure" (zero releases or a
// login error) from a real page. The API key rides in the Authorization header, never
// the URL, and is never logged.
func (d *driver) Search(ctx context.Context, q search.Query) ([]*normalizer.Release, error) {
	resp, err := d.get(ctx, d.buildBrowseURL(q))
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	switch {
	case resp.StatusCode == stdhttp.StatusUnauthorized || resp.StatusCode == stdhttp.StatusForbidden:
		return nil, fmt.Errorf("gazelle: search unauthorized: %w", login.ErrLoginFailed)
	case search.IsRateLimitStatus(resp.StatusCode):
		return nil, &search.RateLimitedError{
			StatusCode: resp.StatusCode,
			RetryAfter: search.ParseRetryAfter(resp.Header.Get("Retry-After"), d.clock),
		}
	case resp.StatusCode < 200 || resp.StatusCode >= 300:
		return nil, fmt.Errorf("gazelle: search returned HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		return nil, fmt.Errorf("gazelle: read search response: %w", err)
	}
	return d.parseBrowse(body)
}

// buildBrowseURL composes the ajax.php?action=browse request URL. order_by=time and
// order_way=desc are always set; searchstr carries the free-text term (dots replaced by
// spaces, per Prowlarr); the fielded music params artistname/groupname/year are set when
// present (artistname is skipped for an empty value or "VA"); and one filter_cat[<id>]=1
// is emitted per requested tracker category. recordlabel is intentionally NOT sent —
// RED/OPS do not advertise or use a Label param (Prowlarr RED/OPS RequestGenerator).
// The URL carries no secret (auth is the Authorization header), so it is safe to log.
func (d *driver) buildBrowseURL(q search.Query) string {
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
	encoded := params.Encode()
	if cats := filterCats(q.Categories); cats != "" {
		encoded += "&" + cats
	}
	return fmt.Sprintf("%sajax.php?%s", d.baseURL, encoded)
}

// sanitizeTerm trims the free-text term and replaces dots with spaces, matching
// Prowlarr's GazelleRequestGenerator term handling (Trim + Replace(".", " ")).
func sanitizeTerm(keywords string) string {
	return strings.ReplaceAll(strings.TrimSpace(keywords), ".", " ")
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
