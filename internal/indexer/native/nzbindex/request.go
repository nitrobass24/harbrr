package nzbindex

import (
	"net/url"
	"strconv"
	"strings"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
)

// defaultLimit is the page size requested when a query carries no explicit limit
// (Prowlarr's `searchCriteria.Limit ?? 100`).
const defaultLimit = 100

// buildSearchURL maps a search.Query onto the NZBIndex search URL, reproducing Prowlarr's
// NzbIndexRequestGenerator:
//
//	{baseUrl}/api/search?max={limit}&key={apikey}&q={term}&p={page}
//
// `max` is the page size; `key` is sent only when configured (public access omits it); `q`
// is the (tv-folded) search term, sent only when non-empty; `p` is the page number
// (offset/limit), sent only when > 0 so a first-page request stays minimal. The apikey is
// secret-bearing and MUST be redacted before any URL is logged or surfaced in an error.
func (d *driver) buildSearchURL(q search.Query) string {
	limit := resolveLimit(q.Limit)
	params := url.Values{}
	params.Set("max", strconv.Itoa(limit))
	if d.apikey != "" {
		params.Set("key", d.apikey)
	}
	if term := searchTerm(q); term != "" {
		params.Set("q", term)
	}
	if q.Offset > 0 {
		if page := q.Offset / limit; page > 0 {
			params.Set("p", strconv.Itoa(page))
		}
	}
	return d.BaseURL + "api/search?" + params.Encode()
}

// resolveLimit picks the upstream page size: the query's explicit limit when positive, else
// the 100-result default. A non-positive limit (a zero Query) falls back so a bare search
// keeps fetching a full page.
func resolveLimit(limit int) int {
	if limit > 0 {
		return limit
	}
	return defaultLimit
}

// searchTerm builds the NZBIndex query term, mirroring Prowlarr's per-criteria
// SanitizedSearchTerm / SanitizedTvSearchString: the keyword plus, for a TV query, the
// season/episode string folded in (NZBIndex has no season/ep params — it is a single q).
func searchTerm(q search.Query) string {
	keyword := strings.TrimSpace(q.Keywords)
	return strings.TrimSpace(keyword + " " + q.EpisodeSearchString())
}
