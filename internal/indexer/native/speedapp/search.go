package speedapp

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf16"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/normalizer"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
	"github.com/autobrr/harbrr/internal/indexer/native"
)

const maxItemsPerPage = 100

// Search returns the requested post-cleanup result window. Keyword searches walk from
// page one because filtering changes offsets; RSS and ID searches can start deep.
func (d *driver) Search(ctx context.Context, q search.Query) ([]*normalizer.Release, error) {
	var (
		releases []*normalizer.Release
		err      error
	)
	if shouldCleanup(q) {
		releases, err = d.filteredWindow(ctx, q)
	} else {
		releases, err = d.directWindow(ctx, q)
	}
	if err != nil {
		return nil, err
	}
	native.TraceReleases(d.Log, d.Def.ID, releases)
	return releases, nil
}

func (d *driver) filteredWindow(ctx context.Context, q search.Query) ([]*normalizer.Release, error) {
	target, perPage := resultLimit(q.Limit), itemsPerPage(q.Limit)
	skip := max(q.Offset, 0)
	out := make([]*normalizer.Release, 0, min(target, perPage))
	for page := 1; ; page++ {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("speedapp: walk filtered pages: %w", err)
		}
		rows, err := d.searchPage(ctx, q, page, page > 1, perPage)
		if err != nil {
			return nil, err
		}
		filtered := cleanup(q.Keywords, rows)
		if skip >= len(filtered) {
			skip -= len(filtered)
		} else {
			filtered = filtered[skip:]
			skip = 0
			out = append(out, filtered...)
		}
		if len(out) >= target {
			return out[:target], nil
		}
		if len(rows) < perPage {
			return out, nil
		}
	}
}

func (d *driver) directWindow(ctx context.Context, q search.Query) ([]*normalizer.Release, error) {
	target, perPage := resultLimit(q.Limit), itemsPerPage(q.Limit)
	page, includePage, skip := 1, false, max(q.Offset, 0)
	if q.Offset > 0 && q.Limit > 0 {
		page = q.Offset/q.Limit + 1
		includePage = true
		skip = q.Offset % q.Limit
	}
	out := make([]*normalizer.Release, 0, min(target, perPage))
	for {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("speedapp: walk direct pages: %w", err)
		}
		rows, err := d.searchPage(ctx, q, page, includePage, perPage)
		if err != nil {
			return nil, err
		}
		rawCount := len(rows)
		if skip >= len(rows) {
			skip -= len(rows)
		} else {
			rows = rows[skip:]
			skip = 0
			out = append(out, rows...)
		}
		if len(out) >= target {
			return out[:target], nil
		}
		if rawCount < perPage {
			return out, nil
		}
		page++
		includePage = true
	}
}

func (d *driver) searchPage(ctx context.Context, q search.Query, page int, includePage bool, perPage int) ([]*normalizer.Release, error) {
	rawURL := d.buildSearchURL(q, page, includePage, perPage)
	resp, token, err := d.bearerGET(ctx, rawURL, "application/json", false)
	if err != nil {
		return nil, err
	}
	releases, err := d.parseReleases(resp.Body)
	return releases, d.ScrubErr(err, token.value)
}

func (d *driver) buildSearchURL(q search.Query, page int, includePage bool, perPage int) string {
	params := url.Values{}
	params.Set("itemsPerPage", strconv.Itoa(perPage))
	params.Set("sort", "torrent.createdAt")
	params.Set("direction", "desc")
	for _, category := range q.Categories {
		if category = strings.TrimSpace(category); category != "" {
			params.Add("categories[]", category)
		}
	}
	if imdb := validIMDBID(q.IMDBID); imdb != "" {
		params.Set("imdbId", imdb)
	} else {
		params.Set("search", strings.TrimSpace(q.Keywords))
	}
	if season := strings.TrimSpace(q.Season); season != "" {
		params.Set("season", season)
	}
	if episode := strings.TrimSpace(q.Ep); episode != "" {
		params.Set("episode", episode)
	}
	if includePage {
		params.Set("page", strconv.Itoa(page))
	}
	return d.BaseURL + "api/torrent?" + params.Encode()
}

func itemsPerPage(limit int) int {
	if limit <= 0 || limit > maxItemsPerPage {
		return maxItemsPerPage
	}
	return limit
}

func resultLimit(limit int) int {
	if limit <= 0 {
		return maxItemsPerPage
	}
	return limit
}

func shouldCleanup(q search.Query) bool {
	return strings.TrimSpace(q.Keywords) != "" && !hasID(q)
}

var imdbPattern = regexp.MustCompile(`^(?:tt)?([0-9]{1,8})$`)

// validIMDBID VALIDATES a caller-supplied id rather than canonicalising it, which is
// why the query path does not use native.CanonicalIMDBID. The kit rescues malformed
// input — "TT123" becomes "tt0000123", a 9-digit id is accepted — and this driver
// depends on the opposite: an id it cannot trust must read as NO id, so the search
// falls back to keyword cleanup instead of asking the tracker about a title the user
// never named (TestInvalidIMDBFallsBackToCleanup). The response path, where the value
// is the tracker's own, does use the kit.
func validIMDBID(raw string) string {
	match := imdbPattern.FindStringSubmatch(strings.TrimSpace(raw))
	if len(match) != 2 {
		return ""
	}
	id, err := strconv.ParseUint(match[1], 10, 64)
	if err != nil || id == 0 {
		return ""
	}
	return fmt.Sprintf("tt%07d", id)
}

func hasID(q search.Query) bool {
	return validIMDBID(q.IMDBID) != "" || q.TMDBID != "" || q.TVDBID != "" || q.TVMazeID != "" ||
		q.TraktID != "" || q.DoubanID != "" || q.RageID != ""
}

var commonWords = map[string]struct{}{
	"and": {},
	"the": {},
	"an":  {},
	"of":  {},
}

func cleanup(query string, releases []*normalizer.Release) []*normalizer.Release {
	terms := queryTerms(query)
	threshold := min(2, len(terms))
	if threshold == 0 {
		return releases
	}
	out := make([]*normalizer.Release, 0, len(releases))
	for _, release := range releases {
		if release == nil {
			continue
		}
		haystack := strings.ToLower(release.Title + "\n" + release.Description)
		matches := 0
		for _, term := range terms {
			if strings.Contains(haystack, strings.ToLower(term)) {
				matches++
			}
		}
		if matches >= threshold {
			out = append(out, release)
		}
	}
	return out
}

func queryTerms(query string) []string {
	parts := strings.FieldsFunc(query, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && !unicode.IsMark(r) && !unicode.Is(unicode.Pc, r)
	})
	terms := parts[:0]
	for _, part := range parts {
		if len(utf16.Encode([]rune(part))) <= 1 {
			continue
		}
		if _, common := commonWords[strings.ToLower(part)]; common {
			continue
		}
		terms = append(terms, part)
	}
	return terms
}
