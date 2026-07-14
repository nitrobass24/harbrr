package torznab

import (
	"encoding/xml"
	"fmt"
	"strconv"
	"strings"

	apphttp "github.com/autobrr/harbrr/internal/http"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/dateparse"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/login"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/mapper"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/normalizer"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
	"github.com/autobrr/harbrr/internal/indexer/native"
)

// torznabAttrNS is the Torznab attribute namespace URI. Feeds bind it to the prefix
// "torznab:", so the parser matches on the namespace URI (via encoding/xml's
// Name.Space resolution) rather than the prefix string — the same idiom the newznab
// sibling uses for its own attribute namespace.
const torznabAttrNS = "http://torznab.com/schemas/2015/feed"

// bittorrentEnclosureType is the MIME type Jackett's MoreThanTVAPI override matches to
// prefer the enclosure's url over <link> (ResultFromFeedItem: `e.Attribute("type").Value
// == "application/x-bittorrent"`).
const bittorrentEnclosureType = "application/x-bittorrent"

// rss is the top-level <rss><channel><item>* envelope plus any descendant <error>,
// mirroring the newznab sibling's envelope: the error element is decoded greedily (it
// can appear at channel or rss level, or as the document root), and items live under
// channel.
type rss struct {
	XMLName xml.Name
	Attrs   []xml.Attr `xml:",any,attr"`
	Error   *apiError  `xml:"error"`
	Channel channel    `xml:"channel"`
}

// channel holds the result items and a channel-level error (some servers place
// <error> inside <channel>).
type channel struct {
	Error *apiError `xml:"error"`
	Items []item    `xml:"item"`
}

// apiError is the Newznab/Torznab error envelope: <error code=".." description=".." />.
// Both are attributes.
type apiError struct {
	Code        string `xml:"code,attr"`
	Description string `xml:"description,attr"`
}

// item is one RSS result row. Enclosure and the torznab:attr set are decoded; Size and
// Files also decode the plain child-element fallback Jackett's base ResultFromFeedItem
// checks (item.FirstValue("size")/("files")) when no attr is present.
type item struct {
	Title       string      `xml:"title"`
	GUID        string      `xml:"guid"`
	Link        string      `xml:"link"`
	Comments    string      `xml:"comments"`
	Description string      `xml:"description"`
	PubDate     string      `xml:"pubDate"`
	Size        string      `xml:"size"`
	Files       string      `xml:"files"`
	Grabs       string      `xml:"grabs"`
	Categories  []string    `xml:"category"`
	Enclosures  []enclosure `xml:"enclosure"`
	Attrs       []tzAttr    `xml:"attr"`
}

// enclosure is the <enclosure url length type/> element.
type enclosure struct {
	URL    string `xml:"url,attr"`
	Length string `xml:"length,attr"`
	Type   string `xml:"type,attr"`
}

// tzAttr is a <torznab:attr name=".." value=".."/> element. Name carries the namespace
// via XMLName.Space so the parser can verify it is the torznab attribute namespace.
type tzAttr struct {
	XMLName xml.Name
	Name    string `xml:"name,attr"`
	Value   string `xml:"value,attr"`
}

// parseReleases decodes a Torznab RSS/XML search response into normalized releases. It
// detects the <error> envelope first (errors are returned with HTTP 200, so the body
// must be inspected even on success) and maps each <item>. An item with no usable
// download link (neither an x-bittorrent enclosure nor a <link>) or no title is
// skipped rather than failing the whole page. A malformed body is an ErrParseError.
func (d *driver) parseReleases(body []byte, catMap *mapper.CategoryMap) ([]*normalizer.Release, error) {
	var feed rss
	if err := xml.Unmarshal(body, &feed); err != nil {
		return nil, fmt.Errorf("torznab: decode search response: %s: %w", apphttp.DecodeErrorDetail(err, body), search.ErrParseError)
	}
	if apiErr := feed.firstError(); apiErr != nil {
		return nil, apiErr.toError(d.apikey)
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

// firstError returns the first <error> found: a bare <error> document root, then a
// child <error> at rss or channel level.
func (f *rss) firstError() *apiError {
	if strings.EqualFold(f.XMLName.Local, "error") {
		return apiErrorFromAttrs(f.Attrs)
	}
	if f.Error != nil {
		return f.Error
	}
	return f.Channel.Error
}

// apiErrorFromAttrs builds an apiError from the attributes of a bare <error> root
// element.
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

// errorCodeAuthLow / errorCodeAuthHigh bound the Torznab "incorrect credentials" code
// range (100-199), matching Prowlarr's TorznabRssParser.PreProcess.
const (
	errorCodeAuthLow  = 100
	errorCodeAuthHigh = 199
)

// toError maps a Torznab error envelope to a Go error, mirroring the newznab
// sibling's toError: a 100-199 code, or a "Request limit reached" / apikey-related
// description, are classified for the registry's health recording. The description is
// server-controlled free text that reaches a persisted health event, so the configured
// apikey is value-scrubbed out of it as defense in depth.
func (e *apiError) toError(apikey string) error {
	desc := scrubAPIKey(strings.TrimSpace(e.Description), apikey)
	if strings.EqualFold(desc, "Request limit reached") {
		return &search.RateLimitedError{StatusCode: 0}
	}
	code, _ := strconv.Atoi(strings.TrimSpace(e.Code))
	if (code >= errorCodeAuthLow && code <= errorCodeAuthHigh) || mentionsAPIKey(desc) {
		return fmt.Errorf("torznab: auth failed (code %s): %s: %w", e.Code, desc, login.ErrLoginFailed)
	}
	return fmt.Errorf("torznab: api error (code %s): %s: %w", e.Code, desc, search.ErrParseError)
}

// scrubAPIKey removes a non-empty apikey value from s so a server echo cannot leak it
// — the same value-scrub idiom as filelist's scrubPasskey.
func scrubAPIKey(s, apikey string) string {
	if apikey == "" {
		return s
	}
	return strings.ReplaceAll(s, apikey, "[redacted]")
}

// mentionsAPIKey reports whether the error description references a missing/incorrect
// apikey, which Prowlarr promotes to an auth failure.
func mentionsAPIKey(desc string) bool {
	return strings.Contains(strings.ToLower(desc), "apikey")
}

// toRelease maps one <item> to a normalized torrent release, or nil when the item
// carries no usable title or download link. Field-by-field this reproduces Jackett's
// BaseNewznabIndexer.ResultFromFeedItem plus MoreThanTVAPI's ResultFromFeedItem
// override (the x-bittorrent enclosure beating <link>, and the seeders/peers/DVF/UVF
// defaults).
func (d *driver) toRelease(it *item, catMap *mapper.CategoryMap) *normalizer.Release {
	title := strings.TrimSpace(it.Title)
	if title == "" {
		return nil
	}
	link := it.downloadLink()
	if link == "" {
		return nil
	}
	return &normalizer.Release{
		Title:       title,
		Description: strings.TrimSpace(it.Description),
		Comments:    strings.TrimSpace(it.Comments),
		Details:     trimComments(it.Comments),
		Link:        link,
		// The upstream <guid> is carried as the stable dedup identity. It is
		// server-controlled free text served verbatim in the feed, so any secret
		// query/userinfo param is scrubbed as defense in depth (RedactURLIdentity,
		// NOT RedactURL — the latter also redacts long hex path tokens, which is
		// exactly the per-release id a details permalink carries; redacting it would
		// collapse every release to one guid and break dedup). Mirrors the newznab
		// sibling's identical GUID handling.
		GUID:                 apphttp.RedactURLIdentity(strings.TrimSpace(it.GUID)),
		Magnet:               it.attr("magneturl"),
		InfoHash:             it.attr("infohash"),
		Size:                 it.size(),
		Categories:           it.category(catMap),
		Grabs:                parseInt64(strings.TrimSpace(it.Grabs)),
		Files:                it.files(),
		Seeders:              nonNegative(it.attrInt("seeders")),
		Leechers:             it.attrInt("leechers"),
		Peers:                nonNegative(it.attrInt("peers")),
		PublishDate:          d.publishDate(it.PubDate),
		DownloadVolumeFactor: attrFactor(it.attr("downloadvolumefactor"), 0),
		UploadVolumeFactor:   attrFactor(it.attr("uploadvolumefactor"), 1),
		IMDBID:               it.imdbID(),
		TVDBID:               it.attrInt("tvdbid"),
		TVMazeID:             it.attrInt("tvmazeid"),
		RageID:               it.attrInt("rageid"),
		Poster:               it.attr("coverurl"),
	}
}

// downloadLink resolves the release's acquisition link: <link> by default, overridden
// by the x-bittorrent enclosure's url when present — MoreThanTVAPI's ResultFromFeedItem
// override (`if (enclosure != null) release.Link = new Uri(enclosureUrl)`).
func (it *item) downloadLink() string {
	if enc := it.bittorrentEnclosureURL(); enc != "" {
		return enc
	}
	return strings.TrimSpace(it.Link)
}

// bittorrentEnclosureURL returns the url of the first application/x-bittorrent
// enclosure, or "" when none is present.
func (it *item) bittorrentEnclosureURL() string {
	for i := range it.Enclosures {
		e := &it.Enclosures[i]
		if strings.EqualFold(strings.TrimSpace(e.Type), bittorrentEnclosureType) {
			if u := strings.TrimSpace(e.URL); u != "" {
				return u
			}
		}
	}
	return ""
}

// size returns the release size: the torznab:attr "size" when present and parseable,
// else the plain <size> child element, else the x-bittorrent enclosure's length
// attribute — Jackett's base ResultFromFeedItem attr-then-element fallback, with the
// enclosure length as the final fallback (mirroring the newznab sibling's GetSize).
func (it *item) size() int64 {
	if n := parsePositiveInt64(it.attr("size")); n > 0 {
		return n
	}
	if n := parsePositiveInt64(strings.TrimSpace(it.Size)); n > 0 {
		return n
	}
	for i := range it.Enclosures {
		if n := parsePositiveInt64(strings.TrimSpace(it.Enclosures[i].Length)); n > 0 {
			return n
		}
	}
	return 0
}

// files returns the release's file count: the torznab:attr "files" when present, else
// the plain <files> child element — Jackett's base ResultFromFeedItem fallback.
func (it *item) files() int64 {
	if n := parsePositiveInt64(it.attr("files")); n > 0 {
		return n
	}
	return parsePositiveInt64(strings.TrimSpace(it.Files))
}

// parsePositiveInt64 parses s as a base-10 int64, returning 0 on blank/unparseable/
// non-positive input.
func parsePositiveInt64(s string) int64 {
	if s == "" {
		return 0
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n <= 0 {
		return 0
	}
	return n
}

// category resolves the release's single category id: the LAST numeric <category>
// child element when any are present, else the FIRST torznab:attr "category" value —
// Jackett's base ResultFromFeedItem rule exactly (`categories.Last(...)` vs
// `attributes.First(...)`) — mapped through the driver's CategoryMap (the preset's
// pass-through 1:1 mapping table).
func (it *item) category(catMap *mapper.CategoryMap) []int {
	raw := it.lastNumericCategory()
	if raw == "" {
		raw = it.attr("category")
	}
	if raw == "" || catMap == nil {
		return nil
	}
	return catMap.MapTrackerCatToNewznab(raw)
}

// lastNumericCategory returns the last <category> child element whose value parses as
// an integer, or "" when none do.
func (it *item) lastNumericCategory() string {
	var last string
	for _, c := range it.Categories {
		c = strings.TrimSpace(c)
		if c == "" {
			continue
		}
		if _, err := strconv.Atoi(c); err == nil {
			last = c
		}
	}
	return last
}

// nonNegative clamps a parsed seeders/peers value to 0 when it is not strictly
// positive — Jackett's MoreThanTVAPI override (`release.Seeders > 0 ? release.Seeders :
// 0`, likewise for Peers). Absent attrs already parse to 0 via attrInt, so this only
// changes behavior for an explicit non-positive value.
func nonNegative(v int64) int64 {
	if v > 0 {
		return v
	}
	return 0
}

// attrFactor parses a DVF/UVF attr value, falling back to fallback when the attr is
// absent, unparseable, or not strictly positive — Jackett's MoreThanTVAPI override
// (`release.DownloadVolumeFactor > 0 ? release.DownloadVolumeFactor : 0`, likewise
// UploadVolumeFactor defaulting to 1). Note the DVF fallback of 0 (not the usual "1.0
// means normal cost" convention every other harbrr driver defaults to absent-DVF to) is
// a literal, intentional reproduction of Jackett's MTV override — not a bug.
func attrFactor(raw string, fallback float64) float64 {
	v, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
	if err != nil || v <= 0 {
		return fallback
	}
	return v
}

// imdbID renders the release's IMDb id in harbrr's canonical "tt0000000" feed form,
// preferring the bare-digits "imdb" attr and falling back to the "tt"-prefixed
// "imdbid" attr — Jackett's base ResultFromFeedItem preference order. A missing or
// unparseable value yields "".
func (it *item) imdbID() string {
	if n := parsePositiveInt64(it.attr("imdb")); n > 0 {
		return fmt.Sprintf("tt%07d", n)
	}
	digits := strings.TrimPrefix(strings.ToLower(it.attr("imdbid")), "tt")
	if n := parsePositiveInt64(digits); n > 0 {
		return fmt.Sprintf("tt%07d", n)
	}
	return ""
}

// publishDate renders the item's <pubDate> as a canonical RFC3339 string via the
// repo's shared native-driver date parser (the same idiom gazelle/gazellegames use for
// their own feed dates), tolerating both RFC1123Z-style RSS dates (the real MoreThanTV
// capture's form) and any other absolute/relative form the parser understands. An
// unparseable value yields "".
func (d *driver) publishDate(value string) string {
	v := strings.TrimSpace(value)
	if v == "" {
		return ""
	}
	out, err := dateparse.New(dateparse.WithClock(d.Clock)).ParseRelTime(v)
	if err != nil {
		return ""
	}
	return out
}

// trimComments strips a trailing "#comments" fragment from a comments URL, yielding
// the details URL — the same idiom the newznab sibling uses (Prowlarr's GetInfoUrl).
func trimComments(comments string) string {
	return strings.TrimSuffix(strings.TrimSpace(comments), "#comments")
}

// attr returns the value of the first torznab:attr with the given name
// (case-insensitive on name, namespace-matched on the attr element). A missing attr
// yields "".
func (it *item) attr(name string) string {
	for i := range it.Attrs {
		a := &it.Attrs[i]
		if a.isTorznab() && strings.EqualFold(a.Name, name) {
			return strings.TrimSpace(a.Value)
		}
	}
	return ""
}

// attrInt returns the first torznab:attr with the given name parsed as int64 (0 when
// absent or unparseable).
func (it *item) attrInt(name string) int64 {
	return parseInt64(it.attr(name))
}

// isTorznab reports whether the attr element is in the torznab attribute namespace.
// Some minimal feeds omit the namespace binding (XMLName.Space == ""); those are
// accepted too, so a feed that only declares the default RSS namespace still parses.
func (a *tzAttr) isTorznab() bool {
	return a.XMLName.Space == torznabAttrNS || a.XMLName.Space == ""
}

// parseInt64 parses s as a base-10 int64, returning 0 on blank/unparseable input
// (unlike parsePositiveInt64, a valid zero or negative value is preserved — used only
// for id-style attrs where 0 is a legitimate "absent" sentinel already).
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
