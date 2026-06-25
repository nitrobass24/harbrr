package passthepopcorn

import (
	"context"
	"fmt"
	"io"
	"mime"
	stdhttp "net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/login"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/normalizer"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
)

// maxBodyBytes caps a torrents.php?action=advanced response. A PTP page is small JSON
// (PageSize 50 movie groups, each with a handful of nested torrents), so this is generous
// while still bounding a hostile or runaway body.
const maxBodyBytes = 8 << 20 // 8 MiB

// fixed query params on every search request, confirmed in Prowlarr
// PassThePopcornRequestGenerator.GetRequest: action=advanced selects the JSON search,
// json=noredirect returns the body inline, grouping=0 yields one torrent per row (still
// nested under Movies[]), and order_by=time / order_way=desc give the newest-first feed.
const (
	paramAction   = "advanced"
	paramJSON     = "noredirect"
	paramGrouping = "0"
	paramOrderBy  = "time"
	paramOrderWay = "desc"
)

// Search issues the authenticated torrents.php?action=advanced request for the query and
// returns the parsed releases. A 401 (bad creds) is an auth failure wrapped with
// login.ErrLoginFailed (so the registry records an auth_failure health event); a 403
// (PTP's query-limit) or a 429/503 is a RateLimitedError carrying any Retry-After — the
// parity target (Prowlarr's PassThePopcornParser) raises RequestLimitReachedException on
// 403, a transient pacing signal, not bad creds; any other non-2xx is an error. A 200 body
// must be JSON (Prowlarr rejects a non-JSON response) and is handed to parseReleases. The
// ApiUser/ApiKey ride in headers, never the URL, and are never logged.
func (d *driver) Search(ctx context.Context, q search.Query) ([]*normalizer.Release, error) {
	resp, err := d.get(ctx, d.buildSearchURL(q), "application/json")
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	switch {
	case resp.StatusCode == stdhttp.StatusUnauthorized:
		return nil, fmt.Errorf("passthepopcorn: search unauthorized: %w", login.ErrLoginFailed)
	case resp.StatusCode == stdhttp.StatusForbidden || search.IsRateLimitStatus(resp.StatusCode):
		return nil, &search.RateLimitedError{
			StatusCode: resp.StatusCode,
			RetryAfter: search.ParseRetryAfter(resp.Header.Get("Retry-After"), d.clock),
		}
	case resp.StatusCode < 200 || resp.StatusCode >= 300:
		return nil, fmt.Errorf("passthepopcorn: search returned HTTP %d", resp.StatusCode)
	}

	if !isJSONContentType(resp.Header.Get("Content-Type")) {
		return nil, fmt.Errorf("passthepopcorn: search returned non-JSON response: %w", search.ErrParseError)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		return nil, fmt.Errorf("passthepopcorn: read search response: %w", err)
	}
	return d.parseReleases(body)
}

// buildSearchURL composes the torrents.php?action=advanced request URL. The five fixed
// params (action, json, grouping, order_by, order_way) are always set; searchstr carries
// the search term — the full "tt"-prefixed imdb id when an imdb query, else the free-text
// keyword (Prowlarr passes searchCriteria.FullImdbId as the searchstr for an imdb search).
// The URL carries no secret (auth is the ApiUser/ApiKey headers), so it is safe to log.
func (d *driver) buildSearchURL(q search.Query) string {
	params := url.Values{}
	params.Set("action", paramAction)
	params.Set("json", paramJSON)
	params.Set("grouping", paramGrouping)
	params.Set("order_by", paramOrderBy)
	params.Set("order_way", paramOrderWay)
	if term := searchTerm(q); term != "" {
		params.Set("searchstr", term)
	}
	return fmt.Sprintf("%storrents.php?%s", d.baseURL, params.Encode())
}

// searchTerm resolves the searchstr value: the full "tt"-prefixed imdb id when the query
// carries one (Prowlarr uses searchCriteria.FullImdbId as the search term), else the
// trimmed free-text keyword. An imdb query has no separate param — the id goes in the same
// searchstr slot. An empty result is a browse/RSS query (no searchstr).
func searchTerm(q search.Query) string {
	if imdb := fullIMDBID(q.IMDBID); imdb != "" {
		return imdb
	}
	return strings.TrimSpace(q.Keywords)
}

// fullIMDBID renders an imdb id as Prowlarr's FullImdbId ("tt" + the numeric id, a minimum
// of seven digits): a leading "tt" is stripped, the rest parsed and zero-padded. A
// non-numeric or empty id yields "" (no imdb search). Mirrors filelist.fullIMDBID.
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

// isJSONContentType reports whether a Content-Type names an application/json body
// (parameters and casing ignored). Prowlarr rejects a non-application/json PTP response as
// an error before parsing; an empty/absent Content-Type is treated as non-JSON. A bare
// type that fails to parse falls back to a prefix check so a slightly malformed header
// ("application/json" with stray bytes) is still accepted.
func isJSONContentType(contentType string) bool {
	ct := strings.TrimSpace(contentType)
	if ct == "" {
		return false
	}
	if mt, _, err := mime.ParseMediaType(ct); err == nil {
		return mt == "application/json"
	}
	return strings.HasPrefix(strings.ToLower(ct), "application/json")
}
