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
// attributes.
type apiError struct {
	Code        string `xml:"code,attr"`
	Description string `xml:"description,attr"`
}

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
		return nil, apiErr.toError()
	}
	releases := make([]*normalizer.Release, 0, len(feed.Channel.Items))
	for i := range feed.Channel.Items {
		if rel := toRelease(&feed.Channel.Items[i], catMap); rel != nil {
			releases = append(releases, rel)
		}
	}
	return releases, nil
}

// firstError returns the first <error> found: a bare <error> document root, then a child
// <error> at rss or channel level. A bare root carries its code/description on the captured
// root attributes (the child mapping does not match the root element itself).
func (f *rss) firstError() *apiError {
	if strings.EqualFold(f.XMLName.Local, "error") {
		return apiErrorFromAttrs(f.Attrs)
	}
	if f.Error != nil {
		return f.Error
	}
	return f.Channel.Error
}

// apiErrorFromAttrs builds an apiError from the attributes of a bare <error> root element.
func apiErrorFromAttrs(attrs []xml.Attr) *apiError {
	e := &apiError{}
	for _, a := range attrs {
		switch strings.ToLower(a.Name.Local) {
		case "code":
			e.Code = a.Value
		case "description":
			e.Description = a.Value
		}
	}
	return e
}

// errorCodeAuthLow / errorCodeAuthHigh bound the Newznab "incorrect credentials" code range
// (100-199), which is an auth failure. 200-299 is a bad/missing parameter; 300-399 is a
// content error; 900-999 is a generic/unknown error.
const (
	errorCodeAuthLow  = 100
	errorCodeAuthHigh = 199
)

// toError maps a Newznab error envelope to a Go error. A 100-199 code, or a "Request limit
// reached" / apikey-related description, are classified for the registry's health
// recording: auth failures unwrap to login.ErrLoginFailed, rate limits to a RateLimitedError;
// every other code is a generic parse error. The description is server-controlled text and
// carries no harbrr secret (the apikey is never echoed back in the description), so it is
// surfaced as-is.
func (e *apiError) toError() error {
	desc := strings.TrimSpace(e.Description)
	if strings.EqualFold(desc, "Request limit reached") {
		return &search.RateLimitedError{StatusCode: 0}
	}
	code, _ := strconv.Atoi(strings.TrimSpace(e.Code))
	if (code >= errorCodeAuthLow && code <= errorCodeAuthHigh) || mentionsAPIKey(desc) {
		return fmt.Errorf("newznab: auth failed (code %s): %s: %w", e.Code, desc, login.ErrLoginFailed)
	}
	return fmt.Errorf("newznab: api error (code %s): %s: %w", e.Code, desc, search.ErrParseError)
}

// mentionsAPIKey reports whether the error description references a missing/incorrect
// apikey, which Prowlarr promotes to an auth failure (e.g. code 200 "Missing parameter:
// apikey").
func mentionsAPIKey(desc string) bool {
	return strings.Contains(strings.ToLower(desc), "apikey")
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
		Details:     trimComments(it.Comments),
		Link:        nzbURL,
		// Carry the upstream <guid> as the stable dedup identity (churn-immune to
		// volatile download URLs). It is normally a passkey-free release id / details
		// permalink, but the <guid> is server-controlled free text and this value is
		// served verbatim in the feed, so redact any secret query params as defense in
		// depth — a misbehaving server that uses an apikey-bearing download URL as its
		// guid must not leak it. RedactURL leaves bare ids and clean permalinks intact,
		// so dedup stays stable and churn-immune.
		GUID:        apphttp.RedactURL(strings.TrimSpace(it.GUID)),
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
	rel.TMDBID = parseInt64(firstNonEmpty(it.attr("tmdbid"), it.attr("tmdb")))
	rel.TVDBID = parseInt64(firstNonEmpty(it.attr("tvdbid"), it.attr("tvdb")))
	rel.TVMazeID = parseInt64(firstNonEmpty(it.attr("tvmazeid"), it.attr("tvmaze")))
	rel.TraktID = parseInt64(firstNonEmpty(it.attr("traktid"), it.attr("trakt")))
	rel.RageID = parseInt64(it.attr("rageid"))
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
	return parseInt64(it.attr(name))
}

// isNewznab reports whether the attr element is in the newznab attribute namespace. Some
// minimal feeds omit the namespace binding (XMLName.Space == ""); those are accepted too, so
// a feed that only declares the default RSS namespace still parses (the attr name is still
// the disambiguator).
func (a *nzbAttr) isNewznab() bool {
	return a.XMLName.Space == newznabAttrNS || a.XMLName.Space == ""
}

// trimComments strips a trailing "#comments" fragment from a comments URL (Prowlarr's
// GetInfoUrl), yielding the details URL.
func trimComments(comments string) string {
	c := strings.TrimSpace(comments)
	return strings.TrimSuffix(c, "#comments")
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

// parseInt64 parses s as a base-10 int64, returning 0 on blank/unparseable input.
func parseInt64(s string) int64 {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0
	}
	return n
}
