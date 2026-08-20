package xspeeds

import (
	"context"
	"fmt"
	stdhttp "net/http"
	"net/url"
	"regexp"
	"strings"
	"unicode"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/login"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/normalizer"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
)

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
	request, err := stdhttp.NewRequestWithContext(ctx, stdhttp.MethodGet, d.BaseURL+"browse.php?"+params.Encode(), nil)
	if err != nil {
		return nil, fmt.Errorf("xspeeds: build browse request: %w", err)
	}
	request.Header.Set("Accept", "text/html")
	return request, nil
}

func singleCategory(categories []string) string {
	seen := make(map[string]struct{}, len(categories))
	var distinct []string
	for _, category := range categories {
		category = strings.TrimSpace(category)
		if category == "" {
			continue
		}
		if _, exists := seen[category]; exists {
			continue
		}
		seen[category] = struct{}{}
		distinct = append(distinct, category)
	}
	if len(distinct) == 1 {
		return distinct[0]
	}
	return "0"
}

func buildSearchTerm(query search.Query) string {
	term := sanitizeSearchTerm(query.Keywords)
	if episode := query.EpisodeSearchString(); episode != "" {
		term = strings.TrimSpace(term + " " + episode)
	}
	return strings.TrimSpace(searchSeparators.ReplaceAllString(term, " "))
}

func sanitizeSearchTerm(term string) string {
	var builder strings.Builder
	builder.Grow(len(term))
	previousDash := false
	for _, char := range term {
		if unicode.Is(unicode.Pd, char) {
			if !previousDash {
				builder.WriteByte('-')
			}
			previousDash = true
			continue
		}
		previousDash = false
		switch char {
		case '\u0060', '\u00b4', '\u2018', '\u2019':
			char = '\''
		}
		if unicode.IsLetter(char) || unicode.IsDigit(char) || unicode.IsSpace(char) || strings.ContainsRune("-._()@/'[]+%", char) {
			builder.WriteRune(char)
		}
	}
	return builder.String()
}
