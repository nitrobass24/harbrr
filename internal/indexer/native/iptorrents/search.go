package iptorrents

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/normalizer"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
)

// searchPath is the IPTorrents torrent-list endpoint (relative to the base URL).
const searchPath = "t"

// Search issues the IPTorrents list request for the query and returns the parsed
// releases. A 401/403 is an auth failure; a 429/503 is a rate-limit error; any other
// non-2xx is an error. The cookie + User-Agent ride as headers (added by get), never
// the URL, so the served (recorded) URL carries no secret.
func (d *driver) Search(ctx context.Context, q search.Query) ([]*normalizer.Release, error) {
	resp, err := d.get(ctx, d.buildSearchURL(q), "text/html", false)
	if err != nil {
		return nil, err
	}
	return d.parseReleases(resp.Body)
}

// buildSearchURL renders the IPTorrents list request, matching Prowlarr's
// IPTorrentsRequestGenerator.GetPagedRequests: each resolved tracker category is a
// query param whose NAME is the category id and value is empty (`?72=&73=`), an
// optional `free=on` for freeleech, the Sphinx-grouped `q=+(imdb)` + `qf=all` when an
// imdb id is present, and the Sphinx-grouped `q=+(term)` for the keyword/episode term.
// harbrr fetches a single page, so the `p` page param is omitted.
//
// Note: NameValueCollection allows the same key twice (imdb + term both add `q`), but
// harbrr's url.Values would collapse them; this driver builds the single combined `q`
// the two branches produce in practice (an imdb query carries no separate keyword
// term, a keyword query carries no imdb). See the testdata README divergence note.
func (d *driver) buildSearchURL(q search.Query) string {
	params := url.Values{}
	for _, cat := range distinct(q.Categories) {
		params.Set(cat, "")
	}
	if freeleechOnly(d.Cfg) {
		params.Set("free", "on")
	}
	imdb := fullIMDBID(q.IMDBID)
	if imdb != "" {
		params.Set("q", sphinx(imdb))
		params.Set("qf", "all")
	}
	if term := d.searchTerm(q); term != "" {
		params.Set("q", sphinx(term))
	}

	raw := d.BaseURL + searchPath
	if len(params) > 0 {
		raw += "?" + params.Encode()
	}
	return raw
}

// searchTerm builds the IPTorrents search term, mirroring Prowlarr's per-criteria
// SanitizedSearchTerm / SanitizedTvSearchString: the keyword plus, for a TV query, the
// season/episode string. A season-only TV query gets a trailing `*` (Prowlarr's
// wildcard for "all episodes of a season"). The result is trimmed.
func (d *driver) searchTerm(q search.Query) string {
	keyword := strings.TrimSpace(q.Keywords)
	season := strings.TrimSpace(q.Season)
	ep := strings.TrimSpace(q.Ep)
	if season == "" && ep == "" {
		return keyword
	}
	epString := episodeSearchString(season, ep)
	term := strings.TrimSpace(keyword + " " + epString)
	if season != "" && season != "0" && ep == "" {
		term += "*"
	}
	return strings.TrimSpace(term)
}

// episodeSearchString reproduces TvSearchCriteria's SxxExx rendering: a season with no
// episode is "S{season:00}"; a season+episode is "S{season:00}E{episode:00}"; a
// seasonless query is empty. Non-numeric season/episode values fall back to the raw
// value (matching ParseUtil's lenient coercion).
func episodeSearchString(season, episode string) string {
	if season == "" || season == "0" {
		return ""
	}
	seasonPart := season
	if n, err := strconv.Atoi(season); err == nil {
		seasonPart = fmt.Sprintf("%02d", n)
	}
	if episode == "" {
		return "S" + seasonPart
	}
	if n, err := strconv.Atoi(episode); err == nil {
		return fmt.Sprintf("S%sE%02d", seasonPart, n)
	}
	return "S" + seasonPart + "E" + episode
}

// sphinx wraps a term in IPTorrents' Sphinx boolean grouping `+(term)`.
func sphinx(term string) string { return "+(" + term + ")" }

// fullIMDBID renders an imdb id as Prowlarr's FullImdbId ("tt" + the numeric id, a
// minimum of seven digits). A leading "tt" is stripped, the rest parsed and
// zero-padded. A non-numeric id yields "" (no imdb param is sent).
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

// distinct returns the input with duplicate tracker categories removed, preserving
// order (Prowlarr's MapTorznabCapsToTrackers(...).Distinct()).
func distinct(cats []string) []string {
	seen := make(map[string]struct{}, len(cats))
	out := make([]string, 0, len(cats))
	for _, c := range cats {
		if _, dup := seen[c]; dup {
			continue
		}
		seen[c] = struct{}{}
		out = append(out, c)
	}
	return out
}

// freeleechOnly reports whether the freeleech_only checkbox is enabled. harbrr stores a
// checked checkbox as Jackett's "True" sentinel; common truthy spellings are accepted
// so whatever the management API persists is interpreted consistently.
func freeleechOnly(cfg map[string]string) bool {
	switch strings.ToLower(strings.TrimSpace(cfg["freeleech_only"])) {
	case "true", "1", "on", "yes":
		return true
	default:
		return false
	}
}
