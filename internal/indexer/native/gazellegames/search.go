package gazellegames

import (
	"context"
	"net/url"
	"strings"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/normalizer"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
)

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

	// paramArtistCheck carries one requested category per value (the platform name); GGn's
	// search filters the artist/platform set on it. paramFreeTorrent=1 restricts to freeleech.
	paramArtistCheck = "artistcheck[]"
	paramFreeTorrent = "freetorrent"
)

// Search issues the authenticated api.php search request for the query and returns the
// parsed releases. A 401/403 is an auth failure wrapped with login.ErrLoginFailed (so the
// registry records an auth_failure health event); a rate-limit status is a RateLimitedError
// carrying any Retry-After; any other non-2xx is an error. A 200 body is handed to
// parseSearch, which distinguishes a non-"success" status (a login or parse error) from a
// real page. The API key rides in the X-API-Key header, never the URL, and is never logged.
func (d *driver) Search(ctx context.Context, q search.Query) ([]*normalizer.Release, error) {
	// Ensure the download passkey is fetched before parsing builds each release's download
	// URL (torrent_pass); without it the served Link would carry an empty passkey that GGn
	// rejects. A configured/already-fetched passkey is reused without a round-trip.
	if err := d.ensurePasskey(ctx); err != nil {
		return nil, err
	}
	resp, err := d.get(ctx, d.buildSearchURL(q), false)
	if err != nil {
		return nil, err
	}
	return d.parseSearch(resp.Body)
}

// buildSearchURL composes the api.php?request=search request URL. The static
// request/search_type/empty_groups/order_by/order_way params are always set; the free-text
// term rides in searchstr when present. The requested categories ride as artistcheck[]
// (one per resolved tracker category — for GGn the platform NAME, e.g. "Windows"), and the
// freeleech_only setting adds freetorrent=1, mirroring Prowlarr's
// GazelleGamesRequestGenerator. The URL carries no secret (auth is the X-API-Key header),
// so it is safe to log.
func (d *driver) buildSearchURL(q search.Query) string {
	params := url.Values{}
	params.Set("request", paramRequest)
	params.Set("search_type", paramSearchType)
	params.Set("empty_groups", paramEmptyGroups)
	params.Set("order_by", paramOrderBy)
	params.Set("order_way", paramOrderWay)
	// Prowlarr replaces '.' with ' ' before sending (GazelleGames.GetBasicSearchParameters:
	// searchTerm.Replace(".", " ")): *arr emits dotted scene-style queries and GGn tokenizes
	// the term on spaces, so a dotted query must be de-dotted to match the same releases.
	if term := strings.ReplaceAll(strings.TrimSpace(q.Keywords), ".", " "); term != "" {
		params.Set("searchstr", term)
	}
	d.addCategoryParams(params, q)
	if d.freeleechOnly() {
		params.Set(paramFreeTorrent, "1")
	}
	return d.BaseURL + searchPath + "?" + params.Encode()
}

// addCategoryParams appends each resolved tracker category as an artistcheck[] value
// (Prowlarr's GazelleGamesRequestGenerator: queryCats.ForEach(c => parameters.Add("artistcheck[]", c))).
// q.Categories is already the resolved tracker-category list (the registry's buildQuery ran
// MapTorznabCapsToTrackers), which for GGn is the platform NAME the artist-name search filter
// expects. Values are de-duplicated and added in order; url.Values keeps the bracketed key
// repeated (artistcheck[]=Windows&artistcheck[]=Linux).
func (d *driver) addCategoryParams(params url.Values, q search.Query) {
	seen := make(map[string]struct{}, len(q.Categories))
	for _, c := range q.Categories {
		c = strings.TrimSpace(c)
		if c == "" {
			continue
		}
		if _, dup := seen[c]; dup {
			continue
		}
		seen[c] = struct{}{}
		params.Add(paramArtistCheck, c)
	}
}

// freeleechOnly reports whether the freeleech_only checkbox is enabled. harbrr stores a
// checked checkbox as Jackett's "True" sentinel; common truthy spellings are accepted so
// whatever the management API persists is interpreted consistently. cfg is read under the
// mutex (cfgValue) since fetchPasskey mutates the shared map.
func (d *driver) freeleechOnly() bool {
	switch strings.ToLower(strings.TrimSpace(d.cfgValue("freeleech_only"))) {
	case "true", "1", "on", "yes":
		return true
	default:
		return false
	}
}
