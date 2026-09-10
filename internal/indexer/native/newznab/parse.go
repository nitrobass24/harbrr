package newznab

import (
	"cmp"
	"encoding/xml"
	"fmt"
	"strconv"
	"strings"

	apphttp "github.com/autobrr/harbrr/internal/http"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/mapper"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/normalizer"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
	"github.com/autobrr/harbrr/internal/indexer/native"
)

// newznabAttrNS is the newznab attribute namespace URI. Feeds bind it to the prefix
// "newznab:" and Torznab to "torznab:", so the parser matches on the namespace URI (via
// encoding/xml's Name.Space resolution) rather than the prefix string.
const newznabAttrNS = "http://www.newznab.com/DTD/2010/feeds/attributes/"

// nzbEnclosureType is the MIME type a Newznab enclosure carries for the .nzb link.
const nzbEnclosureType = "application/x-nzb"

// errorCodeDailyQuota is dognzb's documented newznab code for "Daily API limit
// reached" — a tracker-declared request-quota cap, not an ordinary transient
// rate-limit. Kept as a single exact code (not the whole 900-999 "generic/unknown"
// band): only this code is documented as a quota cap by a vendor, so classifying the
// rest of that band as quota-exceeded would be guessing at other trackers' unrelated
// 9xx error meanings (autobrr/harbrr#251 asks to be conservative here). Extend this
// with more codes only once another vendor's quota code is similarly documented. It is
// passed to native.APIEnvelopeError rather than living there: the torznab family
// documents no quota code and passes 0.
const errorCodeDailyQuota = 910

// item is one RSS result row, carried by the shared native.Feed envelope. The attr
// namespace is matched by URI in attr/attrAll so a torznab: feed parses identically.
type item struct {
	Title       string                 `xml:"title"`
	GUID        string                 `xml:"guid"`
	Link        string                 `xml:"link"`
	Comments    string                 `xml:"comments"`
	Description string                 `xml:"description"`
	PubDate     string                 `xml:"pubDate"`
	Categories  []string               `xml:"category"`
	Enclosures  []native.FeedEnclosure `xml:"enclosure"`
	Attrs       []native.FeedAttr      `xml:"attr"`
}

// parseReleases decodes a Newznab RSS/XML search response into normalized releases. It
// detects the <error> envelope first (per the response contract, errors are returned with
// HTTP 200, so the body must be inspected even on success) and maps each <item> with an
// application/x-nzb enclosure to a *normalizer.Release. Items without an nzb enclosure are
// skipped (Prowlarr's ProcessItem returns null). A malformed body is an ErrParseError.
func (d *driver) parseReleases(body []byte, catMap *mapper.CategoryMap) ([]*normalizer.Release, error) {
	var feed native.Feed[item]
	if err := xml.Unmarshal(body, &feed); err != nil {
		return nil, fmt.Errorf("newznab: decode search response: %s: %w", apphttp.DecodeErrorDetail(err, body), search.ErrParseError)
	}
	if apiErr := feed.FirstError(); apiErr != nil {
		return nil, native.APIEnvelopeError("newznab", apiErr, d.apikey, errorCodeDailyQuota)
	}
	releases := make([]*normalizer.Release, 0, len(feed.Channel.Items))
	for i := range feed.Channel.Items {
		if rel := d.toRelease(&feed.Channel.Items[i], catMap); rel != nil {
			releases = append(releases, rel)
		}
	}
	native.TraceReleases(d.Log, d.Def.ID, releases)
	return releases, nil
}

// toRelease maps one <item> to a normalized usenet release, or nil when the item carries no
// nzb enclosure (no download link — Prowlarr skips it). Seeders/Leechers/Peers, Magnet,
// InfoHash, and the volume factors stay zero (usenet has no ratio economy); the serializer
// omits them for a usenet feed. The enclosure url is stored as Release.Link so the /dl grab
// proxy can hand it to Grab — it is the apikey-bearing secret link and never reaches the
// feed bare.
func (d *driver) toRelease(it *item, catMap *mapper.CategoryMap) *normalizer.Release {
	nzbURL := it.nzbURL()
	if nzbURL == "" {
		return nil
	}
	title := strings.TrimSpace(it.Title)
	if title == "" {
		return nil
	}
	rel := &normalizer.Release{
		Title:       title,
		Description: strings.TrimSpace(it.Description),
		Comments:    strings.TrimSpace(it.Comments),
		Details:     native.TrimComments(it.Comments),
		Link:        nzbURL,
		// Carry the upstream <guid> as the stable dedup identity (churn-immune to
		// volatile download URLs). It is normally a passkey-free release id / details
		// permalink, but the <guid> is server-controlled free text and this value is
		// served verbatim in the feed, so scrub any secret query params as defense in
		// depth — a misbehaving server that uses an apikey-bearing download URL as its
		// guid must not leak it. Use RedactURLIdentity (query/userinfo secrets only),
		// NOT RedactURL: RedactURL also redacts long hex path tokens, which is exactly
		// the per-release id in a details permalink (e.g. /details/<32-hex>) — redacting
		// it would collapse every release to one guid and make dedup drop all but one.
		GUID:        apphttp.RedactURLIdentity(strings.TrimSpace(it.GUID)),
		Size:        it.size(),
		Categories:  it.categories(catMap),
		Grabs:       it.attrInt("grabs"),
		Files:       it.attrInt("files"),
		PublishDate: d.publishDate(it.rawPublishDate()),
		Poster:      strings.TrimSpace(it.attr("coverurl")),
	}
	it.fillIDs(rel)
	return rel
}

// nzbURL returns the download URL of the application/x-nzb enclosure. Prowlarr prefers the
// nzb-typed enclosure; if none is explicitly typed it falls back to the first enclosure with
// a url. An item with no enclosure url yields "" (skipped by toRelease).
func (it *item) nzbURL() string {
	var fallback string
	for i := range it.Enclosures {
		enc := &it.Enclosures[i]
		u := strings.TrimSpace(enc.URL)
		if u == "" {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(enc.Type), nzbEnclosureType) {
			return u
		}
		if fallback == "" {
			fallback = u
		}
	}
	return fallback
}

// size returns the release size: the newznab:attr "size" when present and parseable, else
// the application/x-nzb enclosure's length attribute (Prowlarr's GetSize).
func (it *item) size() int64 {
	if s := it.attrInt("size"); s > 0 {
		return s
	}
	for i := range it.Enclosures {
		enc := &it.Enclosures[i]
		if strings.EqualFold(strings.TrimSpace(enc.Type), nzbEnclosureType) || enc.Type == "" {
			if n, err := strconv.ParseInt(strings.TrimSpace(enc.Length), 10, 64); err == nil && n > 0 {
				return n
			}
		}
	}
	return 0
}

// categories resolves the item's category ids to newznab ids via the driver's CategoryMap.
// It prefers the repeatable newznab:attr "category" values; only when none are present does
// it fall back to the plain <category> elements (Prowlarr's GetCategory). Each tracker id is
// mapped through CategoryMap and the custom 1:1 synth ids (>= CustomCategoryOffset) are
// dropped so each release carries standard newznab ids.
func (it *item) categories(catMap *mapper.CategoryMap) []int {
	ids := it.attrAll("category")
	if len(ids) == 0 {
		ids = it.Categories
	}
	out := make([]int, 0, len(ids))
	seen := make(map[int]struct{}, len(ids))
	for _, raw := range ids {
		for _, c := range catMap.MapTrackerCatToNewznab(strings.TrimSpace(raw)) {
			if c >= mapper.CustomCategoryOffset {
				continue
			}
			if _, dup := seen[c]; dup {
				continue
			}
			seen[c] = struct{}{}
			out = append(out, c)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// rawPublishDate returns the release date as the feed wrote it: the newznab:attr
// "usenetdate" overrides <pubDate> when present (Prowlarr's GetPublishDate).
func (it *item) rawPublishDate() string {
	if d := strings.TrimSpace(it.attr("usenetdate")); d != "" {
		return d
	}
	return strings.TrimSpace(it.PubDate)
}

// publishDate normalizes the feed's RFC1123Z-style date to the canonical RFC3339 form
// the torznab serializer and core/aggregate parse, via the shared native-driver helper.
// An unparseable value yields "" and is logged at debug — the torznab sibling's posture
// (autobrr/harbrr#196): the release is kept, and the serializer's now-fallback is not
// left to silently mask a new feed date format.
func (d *driver) publishDate(raw string) string {
	if raw == "" {
		return ""
	}
	out, err := native.PublishDate(raw, d.Clock)
	if err != nil {
		d.Log.Debug().Str("driver", d.Def.ID).Str("value", raw).Err(err).
			Msg("newznab: pubDate parse failed")
		return ""
	}
	return out
}

// fillIDs maps the newznab:attr id values onto the release id fields. Prowlarr tries the
// "imdb"/"imdbid", "tmdbid"/"tmdb", etc. attr-name pairs; harbrr keeps the raw imdb string
// (an int-parse would drop a tt prefix, and harbrr's IMDBID is a string) and parses the
// rest as int64. attr already trims, so cmp.Or picks the first attr that is present.
func (it *item) fillIDs(rel *normalizer.Release) {
	rel.IMDBID = cmp.Or(it.attr("imdb"), it.attr("imdbid"))
	rel.TMDBID = native.ParseInt64(cmp.Or(it.attr("tmdbid"), it.attr("tmdb")))
	rel.TVDBID = native.ParseInt64(cmp.Or(it.attr("tvdbid"), it.attr("tvdb")))
	rel.TVMazeID = native.ParseInt64(cmp.Or(it.attr("tvmazeid"), it.attr("tvmaze")))
	rel.TraktID = native.ParseInt64(cmp.Or(it.attr("traktid"), it.attr("trakt")))
	rel.RageID = native.ParseInt64(it.attr("rageid"))
}

// attr returns the value of the first newznab:attr with the given name (case-insensitive on
// name, namespace-matched on the attr element). A missing attr yields "".
func (it *item) attr(name string) string {
	return native.AttrValue(it.Attrs, newznabAttrNS, name)
}

// attrAll returns all values of the newznab:attr with the given name (repeatable attrs like
// category/language).
func (it *item) attrAll(name string) []string {
	return native.AttrValues(it.Attrs, newznabAttrNS, name)
}

// attrInt returns the first newznab:attr with the given name parsed as int64 (0 when absent
// or unparseable).
func (it *item) attrInt(name string) int64 {
	return native.ParseInt64(it.attr(name))
}
