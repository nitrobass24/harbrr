package gazellegames

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

// maxBodyBytes caps a search response. A GGn search page is small JSON (a bounded set of
// groups, each with a handful of nested torrents), so this is generous while still bounding
// a hostile or runaway body.
const maxBodyBytes = 8 << 20 // 8 MiB

// searchPath is the api.php endpoint Prowlarr's GazelleGamesRequestGenerator hits; the
// driver's baseURL already ends in a single trailing slash.
const searchPath = "api.php"

// Static search params Prowlarr's GetBasicSearchParameters always sets (GazelleGames
// RequestGenerator): request=search selects the search action, search_type=torrents asks
// for the torrent rows, empty_groups=filled drops groups with no torrents, and
// order_by=time / order_way=desc sort newest-first.
const (
	paramRequest     = "search"
	paramSearchType  = "torrents"
	paramEmptyGroups = "filled"
	paramOrderBy     = "time"
	paramOrderWay    = "desc"
)

// Search issues the authenticated api.php search request for the query and returns the
// parsed releases. A 401/403 is an auth failure wrapped with login.ErrLoginFailed (so the
// registry records an auth_failure health event); a rate-limit status is a RateLimitedError
// carrying any Retry-After; any other non-2xx is an error. A 200 body is handed to
// parseSearch, which distinguishes a non-"success" status (a login or parse error) from a
// real page. The API key rides in the X-API-Key header, never the URL, and is never logged.
func (d *driver) Search(ctx context.Context, q search.Query) ([]*normalizer.Release, error) {
	resp, err := d.get(ctx, d.buildSearchURL(q))
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	switch {
	case resp.StatusCode == stdhttp.StatusUnauthorized || resp.StatusCode == stdhttp.StatusForbidden:
		return nil, fmt.Errorf("gazellegames: search unauthorized: %w", login.ErrLoginFailed)
	case search.IsRateLimitStatus(resp.StatusCode):
		return nil, &search.RateLimitedError{
			StatusCode: resp.StatusCode,
			RetryAfter: search.ParseRetryAfter(resp.Header.Get("Retry-After"), d.clock),
		}
	case resp.StatusCode < 200 || resp.StatusCode >= 300:
		return nil, fmt.Errorf("gazellegames: search returned HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		return nil, fmt.Errorf("gazellegames: read search response: %w", err)
	}
	return d.parseSearch(body)
}

// buildSearchURL composes the api.php?request=search request URL. The static
// request/search_type/empty_groups/order_by/order_way params are always set; the free-text
// term rides in searchstr when present. The category filter (artistcheck[]=<platform name>)
// and freetorrent=1 (FreeleechOnly) ride on the deferred platform-name category map and a
// FreeleechOnly setting, neither of which is wired yet, so they are intentionally omitted
// here. The URL carries no secret (auth is the X-API-Key header), so it is safe to log.
func (d *driver) buildSearchURL(q search.Query) string {
	params := url.Values{}
	params.Set("request", paramRequest)
	params.Set("search_type", paramSearchType)
	params.Set("empty_groups", paramEmptyGroups)
	params.Set("order_by", paramOrderBy)
	params.Set("order_way", paramOrderWay)
	if term := strings.TrimSpace(q.Keywords); term != "" {
		params.Set("searchstr", term)
	}
	return d.baseURL + searchPath + "?" + params.Encode()
}
