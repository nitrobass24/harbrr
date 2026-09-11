package iptorrents

import (
	"context"
	"net/url"
	"strings"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/normalizer"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
	"github.com/autobrr/harbrr/internal/indexer/native"
)

// searchPath is the IPTorrents torrent-list endpoint (relative to the base URL).
const searchPath = "t"

// Search issues the IPTorrents list request for the query and returns the parsed
// releases. A 401/403 is an auth failure; a 429/503 is a rate-limit error; any other
// non-2xx is an error, and a 200 login page (no lout.php marker) is an auth failure too.
// The cookie + User-Agent ride as headers (added by get), never the URL, so the served
// (recorded) URL carries no secret.
func (d *driver) Search(ctx context.Context, q search.Query) ([]*normalizer.Release, error) {
	resp, err := d.get(ctx, d.buildSearchURL(q), "text/html", false)
	if err != nil {
		return nil, err
	}
	if err := requireLoggedIn(resp.Body); err != nil {
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
	for _, cat := range q.Categories {
		params.Set(cat, "")
	}
	if native.CheckboxOn(d.Cfg["freeleech_only"]) {
		params.Set("free", "on")
	}
	imdb := native.CanonicalIMDBID(q.IMDBID)
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
	term := strings.TrimSpace(keyword + " " + q.EpisodeSearchString())
	if season != "" && season != "0" && ep == "" {
		term += "*"
	}
	return strings.TrimSpace(term)
}

// sphinx wraps a term in IPTorrents' Sphinx boolean grouping `+(term)`.
func sphinx(term string) string { return "+(" + term + ")" }
