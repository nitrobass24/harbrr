package myanonamouse

import (
	"context"
	"fmt"
	"io"
	stdhttp "net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/login"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/normalizer"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
)

const (
	searchPath = "tor/js/loadSearchJSONbasic.php"
	// perPage is MAM's PageSize (Prowlarr). harbrr pages the served feed itself, so
	// every search fetches one page at offset 0.
	perPage = 100
)

// Search issues the loadSearchJSONbasic.php request for the query and returns the
// parsed releases. A 403 is an auth failure (mam_id expired/invalid), wrapped with
// login.ErrLoginFailed; a 429/503 is a rate-limit error; any other non-2xx is an
// error. The 2xx body is parsed by parseReleases.
func (d *driver) Search(ctx context.Context, q search.Query) ([]*normalizer.Release, error) {
	resp, err := d.get(ctx, d.buildSearchURL(q), "application/json")
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	switch {
	case resp.StatusCode == stdhttp.StatusForbidden:
		return nil, fmt.Errorf("myanonamouse: search forbidden, mam_id expired or invalid: %w", login.ErrLoginFailed)
	case search.IsRateLimitStatus(resp.StatusCode):
		return nil, &search.RateLimitedError{
			StatusCode: resp.StatusCode,
			RetryAfter: search.ParseRetryAfter(resp.Header.Get("Retry-After"), d.clock),
		}
	case resp.StatusCode < 200 || resp.StatusCode >= 300:
		return nil, fmt.Errorf("myanonamouse: search returned HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		return nil, fmt.Errorf("myanonamouse: read search response: %w", err)
	}
	return d.parseReleases(body)
}

// buildSearchURL renders the loadSearchJSONbasic.php request for a query, matching
// Prowlarr's MyAnonamouseRequestGenerator: the keyword in tor[text]; the title/author/
// narrator search-in flags (plus the optional description/series/filenames toggles);
// the constant searchType=all, searchIn=torrents, sortType=default; the numeric
// categories (or "0" for all); perpage=100; startNumber=0; and the
// thumbnails/description/dlLink flags. The mam_id rides as a Cookie header (added by
// get), never the URL, so the URL carries no secret.
func (d *driver) buildSearchURL(q search.Query) string {
	params := url.Values{}
	params.Set("tor[text]", strings.TrimSpace(searchTerm(q)))
	params.Set("tor[searchType]", "all")
	params.Set("tor[searchIn]", "torrents")
	params.Set("tor[sortType]", "default")
	d.addSearchIn(params)
	addCategories(params, q.Categories)
	params.Set("tor[perpage]", strconv.Itoa(perPage))
	params.Set("tor[startNumber]", "0")
	params.Set("thumbnails", "1")
	params.Set("description", "1")
	params.Set("dlLink", "1")
	return d.baseURL + searchPath + "?" + params.Encode()
}

// searchTerm is the free-text keyword. harbrr's search.Query carries the book author/
// title as Keywords already, so the keyword is forwarded as-is (Prowlarr sends the raw
// search term).
func searchTerm(q search.Query) string {
	return q.Keywords
}

// addSearchIn sets the tor[srchIn][…] flags. title/author/narrator are always on
// (Prowlarr's defaults); description/series/filenames are user toggles.
func (d *driver) addSearchIn(params url.Values) {
	params.Set("tor[srchIn][title]", "true")
	params.Set("tor[srchIn][author]", "true")
	params.Set("tor[srchIn][narrator]", "true")
	if boolSetting(d.cfg["search_in_description"]) {
		params.Set("tor[srchIn][description]", "true")
	}
	if boolSetting(d.cfg["search_in_series"]) {
		params.Set("tor[srchIn][series]", "true")
	}
	if boolSetting(d.cfg["search_in_filenames"]) {
		params.Set("tor[srchIn][filenames]", "true")
	}
}

// addCategories sets the tor[cat][n] params for each requested tracker category, or
// tor[cat][]="0" (all) when none was requested, matching Prowlarr.
func addCategories(params url.Values, cats []string) {
	distinct := distinctNonEmpty(cats)
	if len(distinct) == 0 {
		params.Set("tor[cat][]", "0")
		return
	}
	for i, c := range distinct {
		params.Set("tor[cat]["+strconv.Itoa(i)+"]", c)
	}
}

// distinctNonEmpty returns the distinct, non-blank category ids preserving order.
func distinctNonEmpty(cats []string) []string {
	seen := make(map[string]struct{}, len(cats))
	out := make([]string, 0, len(cats))
	for _, c := range cats {
		c = strings.TrimSpace(c)
		if c == "" {
			continue
		}
		if _, dup := seen[c]; dup {
			continue
		}
		seen[c] = struct{}{}
		out = append(out, c)
	}
	return out
}

// boolSetting reports whether a checkbox setting is enabled. harbrr stores a checked
// checkbox as Jackett's "True" sentinel; common truthy spellings are also accepted so
// whatever the management API persists is interpreted consistently.
func boolSetting(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "true", "1", "on", "yes":
		return true
	default:
		return false
	}
}
