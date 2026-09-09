package xspeeds

import (
	"context"
	"fmt"
	stdhttp "net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/login"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/normalizer"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
	"github.com/autobrr/harbrr/internal/indexer/native"
)

// Prowlarr parity: space-through-period range plus underscore.
var searchSeparators = regexp.MustCompile(`[ -._]+`)

// Search performs an authenticated browse and renews the session once when XSpeeds
// reports that the request is logged out. Login is serialized; browse requests are not.
func (d *driver) Search(ctx context.Context, query search.Query) ([]*normalizer.Release, error) {
	return runOperation(ctx, d, "search", func(ctx context.Context, _ sessionState) ([]*normalizer.Release, error) {
		request, err := d.newBrowseRequest(ctx, query)
		if err != nil {
			return nil, err
		}
		response, err := d.Do(noRedirects(ctx), request, classifySession)
		if err != nil {
			return nil, err
		}
		if d.isLoginPage(response.Body) {
			return nil, fmt.Errorf("xspeeds: search returned the login page: %w", login.ErrLoginFailed)
		}
		return d.parseReleases(response.Body)
	})
}

func (d *driver) newBrowseRequest(ctx context.Context, query search.Query) (*stdhttp.Request, error) {
	params := url.Values{
		"category":              {singleCategory(query.Categories)},
		"include_dead_torrents": {"yes"},
		"sort":                  {"added"},
		"order":                 {"desc"},
	}
	if term := buildSearchTerm(query); term != "" {
		params.Set("do", "search")
		params.Set("keywords", term)
		params.Set("search_type", "t_name")
	}
	request, err := d.NewRequest(ctx, stdhttp.MethodGet, d.BaseURL+"browse.php?"+params.Encode(), nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "text/html")
	return request, nil
}

// singleCategory is Prowlarr's FirstIfSingleOrDefault("0"): XSpeeds' browse takes one
// category, so a single requested tracker id passes through and anything else (none, or
// a mix) browses all. q.Categories is already the distinct, non-blank tracker-id mapping
// (mapper.MapTorznabCapsToTrackers).
func singleCategory(categories []string) string {
	if len(categories) == 1 {
		return categories[0]
	}
	return "0"
}

func buildSearchTerm(query search.Query) string {
	term := native.SanitizeSearchTerm(query.Keywords)
	if episode := query.EpisodeSearchString(); episode != "" {
		term = strings.TrimSpace(term + " " + episode)
	}
	return strings.TrimSpace(searchSeparators.ReplaceAllString(term, " "))
}
