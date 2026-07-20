package newznab

import (
	"encoding/xml"
	"fmt"
	"strconv"
	"strings"

	apphttp "github.com/autobrr/harbrr/internal/http"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/login"
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

// rss is the top-level <rss><channel><item>* envelope plus any descendant <error>. The
// error element is decoded greedily (it can appear at channel or rss level), and items live
// under channel. No XMLName constraint is set so the same struct also decodes a bare
// <error> root (some servers return the error envelope as the document root, not nested in
// rss).
type rss struct {
	XMLName xml.Name
	Attrs   []xml.Attr `xml:",any,attr"`
	Error   *apiError  `xml:"error"`
	Channel channel    `xml:"channel"`
}

// channel holds the result items and a channel-level error (some servers place <error>
// inside <channel>).
type channel struct {
	Error *apiError `xml:"error"`
	Items []item    `xml:"item"`
}

// apiError is the Newznab error envelope: <error code=".." description=".." />. Both are
// attributes. Aliased to native.APIError so the decode/lookup logic (FirstError,
// APIErrorFromAttrs, MentionsAPIKey) is shared with the torznab sibling while this struct
// stays package-private per docs/native-indexer-pattern.md.
type apiError = native.APIError

// item is one RSS result row. Enclosure and the newznab:attr set are decoded; the attr
// namespace is matched by URI in attrValue so a torznab: feed parses identically.
type item struct {
	Title       string      `xml:"title"`
	GUID        string      `xml:"guid"`
	Link        string      `xml:"link"`
	Comments    string      `xml:"comments"`
	Description string      `xml:"description"`
	PubDate     string      `xml:"pubDate"`
	Categories  []string    `xml:"category"`
	Enclosures  []enclosure `xml:"enclosure"`
	Attrs       []nzbAttr   `xml:"attr"`
}

// enclosure is the <enclosure url length type/> element. The url of the application/x-nzb
// enclosure is the download link; length is the size fallback.
type enclosure struct {
	URL    string `xml:"url,attr"`
	Length string `xml:"length,attr"`
	Type   string `xml:"type,attr"`
}

// nzbAttr is a <newznab:attr name=".." value=".."/> element. Name carries the namespace via
// XMLName.Space so the parser can verify it is the newznab attribute namespace.
type nzbAttr struct {
	XMLName xml.Name
	Name    string `xml:"name,attr"`
	Value   string `xml:"value,attr"`
}

// parseReleases decodes a Newznab RSS/XML search response into normalized releases. It
// detects the <error> envelope first (per the response contract, errors are returned with
// HTTP 200, so the body must be inspected even on success) and maps each <item> with an
// application/x-nzb enclosure to a *normalizer.Release. Items without an nzb enclosure are
// skipped (Prowlarr's ProcessItem returns null). A malformed body is an ErrParseError.
func (d *driver) parseReleases(body []byte, catMap *mapper.CategoryMap) ([]*normalizer.Release, error) {
	var feed rss
	if err := xml.Unmarshal(body, &feed); err != nil {
		return nil, fmt.Errorf("newznab: decode search response: %s: %w", apphttp.DecodeErrorDetail(err, body), search.ErrParseError)
	}
	if apiErr := feed.firstError(); apiErr != nil {
		return nil, toError(apiErr, d.apikey)
	}
	releases := make([]*normalizer.Release, 0, len(feed.Channel.Items))
	for i := range feed.Channel.Items {
		if rel := toRelease(&feed.Channel.Items[i], catMap); rel != nil {
			releases = append(releases, rel)
		}
	}
	native.TraceReleases(d.Log, d.Def.ID, releases)
	return releases, nil
}

// firstError returns the first <error> found: a bare <error> document root, then a child
// <error> at rss or channel level. A bare root carries its code/description on the captured
// root attributes (the child mapping does not match the root element itself).
func (f *rss) firstError() *apiError {
	return native.FirstError(f.XMLName, f.Attrs, f.Error, f.Channel.Error)
}

// errorCodeAuthLow / errorCodeAuthHigh bound the Newznab "incorrect credentials" code range
// (100-199), which is an auth failure. 200-299 is a bad/missing parameter; 300-399 is a
// content error; 900-999 is a generic/unknown error.
const (
	errorCodeAuthLow  = 100
	errorCodeAuthHigh = 199
)

// errorCodeDailyQuota is dognzb's documented newznab code for "Daily API limit
// reached" — a tracker-declared request-quota cap, not an ordinary transient
// rate-limit. Kept as a single exact code (not the whole 900-999 "generic/unknown"
// band): only this code is documented as a quota cap by a vendor, so classifying the
// rest of that band as quota-exceeded would be guessing at other trackers' unrelated
// 9xx error meanings (autobrr/harbrr#251 asks to be conservative here). Extend this
// with more codes only once another vendor's quota code is similarly documented.
const errorCodeDailyQuota = 910

// toError maps a Newznab error envelope to a Go error. A 100-199 code, or a "Request limit
// reached" / apikey-related description, are classified for the registry's health
// recording: auth failures unwrap to login.ErrLoginFailed, rate limits to a RateLimitedError,
// the dognzb-style daily-quota code to a QuotaExceededError; every other code is a generic
// parse error. The description is server-controlled free text that reaches a persisted
// health event / webhook, so the configured apikey is value-scrubbed out of it as defense in
// depth: a misbehaving server that echoes the submitted apikey back in its description must
// not leak it (a bare "invalid key ABCD1234" would pass RedactError's key[=:]value anchor
// untouched).
//
// A plain function, not a method: apiError is an alias for native.APIError (a type defined
// in another package), and Go forbids attaching methods to a type from outside its home
// package even via a local alias.
func toError(e *apiError, apikey string) error {
	desc := apphttp.ScrubValues(strings.TrimSpace(e.Description), []string{apikey})
	if strings.EqualFold(desc, "Request limit reached") {
		return &search.RateLimitedError{StatusCode: 0}
	}
	code, _ := strconv.Atoi(strings.TrimSpace(e.Code))
	if code == errorCodeDailyQuota {
		return &search.QuotaExceededError{Detail: fmt.Sprintf("newznab: api error (code %d): %s", code, desc)}
	}
	if (code >= errorCodeAuthLow && code <= errorCodeAuthHigh) || native.MentionsAPIKey(desc) {
		return fmt.Errorf("newznab: auth failed (code %s): %s: %w", e.Code, desc, login.ErrLoginFailed)
	}
	return fmt.Errorf("newznab: api error (code %s): %s: %w", e.Code, desc, search.ErrParseError)
}

// toRelease maps one <item> to a normalized usenet release, or nil when the item carries no
// nzb enclosure (no download link — Prowlarr skips it). Seeders/Leechers/Peers, Magnet,
// InfoHash, and the volume factors stay zero (usenet has no ratio economy); the serializer
// omits them for a usenet feed. The enclosure url is stored as Release.Link so the /dl grab
// proxy can hand it to Grab — it is the apikey-bearing secret link and never reaches the
// feed bare.
func toRelease(it *item, catMap *mapper.CategoryMap) *normalizer.Release {
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
		PublishDate: it.publishDate(),
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
			if c >= customCatCutoff {
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

// customCatCutoff bounds the canonical newznab id range: the mapper synthesises a 1:1 custom
// category at ids >= CustomCategoryOffset (100000), which is discarded so a release carries
// only standard newznab categories (matching the HDBits driver).
const customCatCutoff = 100000

// publishDate returns the release date: the newznab:attr "usenetdate" overrides <pubDate>
// when present (Prowlarr's GetPublishDate). The string form is stored as-is for the
// serializer.
func (it *item) publishDate() string {
	if d := strings.TrimSpace(it.attr("usenetdate")); d != "" {
		return d
	}
	return strings.TrimSpace(it.PubDate)
}

// fillIDs maps the newznab:attr id values onto the release id fields. Prowlarr tries the
// "imdb"/"imdbid", "tmdbid"/"tmdb", etc. attr-name pairs; harbrr keeps the raw imdb string
// and parses the rest as int64.
func (it *item) fillIDs(rel *normalizer.Release) {
	rel.IMDBID = imdbAttr(firstNonEmpty(it.attr("imdb"), it.attr("imdbid")))
	rel.TMDBID = native.ParseInt64(firstNonEmpty(it.attr("tmdbid"), it.attr("tmdb")))
	rel.TVDBID = native.ParseInt64(firstNonEmpty(it.attr("tvdbid"), it.attr("tvdb")))
	rel.TVMazeID = native.ParseInt64(firstNonEmpty(it.attr("tvmazeid"), it.attr("tvmaze")))
	rel.TraktID = native.ParseInt64(firstNonEmpty(it.attr("traktid"), it.attr("trakt")))
	rel.RageID = native.ParseInt64(it.attr("rageid"))
}

// attr returns the value of the first newznab:attr with the given name (case-insensitive on
// name, namespace-matched on the attr element). A missing attr yields "".
func (it *item) attr(name string) string {
	for i := range it.Attrs {
		a := &it.Attrs[i]
		if a.isNewznab() && strings.EqualFold(a.Name, name) {
			return strings.TrimSpace(a.Value)
		}
	}
	return ""
}

// attrAll returns all values of the newznab:attr with the given name (repeatable attrs like
// category/language).
func (it *item) attrAll(name string) []string {
	var out []string
	for i := range it.Attrs {
		a := &it.Attrs[i]
		if a.isNewznab() && strings.EqualFold(a.Name, name) {
			if v := strings.TrimSpace(a.Value); v != "" {
				out = append(out, v)
			}
		}
	}
	return out
}

// attrInt returns the first newznab:attr with the given name parsed as int64 (0 when absent
// or unparseable).
func (it *item) attrInt(name string) int64 {
	return native.ParseInt64(it.attr(name))
}

// isNewznab reports whether the attr element is in the newznab attribute namespace. Some
// minimal feeds omit the namespace binding (XMLName.Space == ""); those are accepted too, so
// a feed that only declares the default RSS namespace still parses (the attr name is still
// the disambiguator).
func (a *nzbAttr) isNewznab() bool {
	return a.XMLName.Space == newznabAttrNS || a.XMLName.Space == ""
}

// imdbAttr keeps the raw imdb id string (an int-parse would drop a tt prefix; Prowlarr
// parses to int, but harbrr's IMDBID is a string so the raw value is preserved). A blank
// value yields "".
func imdbAttr(raw string) string {
	return strings.TrimSpace(raw)
}

// firstNonEmpty returns the first non-empty trimmed string.
func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if s := strings.TrimSpace(v); s != "" {
			return s
		}
	}
	return ""
}
