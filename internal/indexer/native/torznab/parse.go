package torznab

import (
	"encoding/xml"
	"fmt"
	"strconv"
	"strings"

	apphttp "github.com/autobrr/harbrr/internal/http"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/dateparse"
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

// item is one RSS result row, carried by the shared native.Feed envelope. Size and
// Files also decode the plain child-element fallback Jackett's base ResultFromFeedItem
// checks (item.FirstValue("size")/("files")) when no attr is present.
type item struct {
	Title       string                 `xml:"title"`
	GUID        string                 `xml:"guid"`
	Link        string                 `xml:"link"`
	Comments    string                 `xml:"comments"`
	Description string                 `xml:"description"`
	PubDate     string                 `xml:"pubDate"`
	Size        string                 `xml:"size"`
	Files       string                 `xml:"files"`
	Grabs       string                 `xml:"grabs"`
	Categories  []string               `xml:"category"`
	Enclosures  []native.FeedEnclosure `xml:"enclosure"`
	Attrs       []native.FeedAttr      `xml:"attr"`
}

// parseReleases decodes a Torznab RSS/XML search response into normalized releases. It
// detects the <error> envelope first (errors are returned with HTTP 200, so the body
// must be inspected even on success) and maps each <item>. An item with no usable
// download link (neither an x-bittorrent enclosure nor a <link>) or no title is
// skipped rather than failing the whole page. A malformed body is an ErrParseError.
func (d *driver) parseReleases(body []byte, catMap *mapper.CategoryMap) ([]*normalizer.Release, error) {
	var feed native.Feed[item]
	if err := xml.Unmarshal(body, &feed); err != nil {
		return nil, fmt.Errorf("torznab: decode search response: %s: %w", apphttp.DecodeErrorDetail(err, body), search.ErrParseError)
	}
	if apiErr := feed.FirstError(); apiErr != nil {
		// quotaCode 0: no torznab-family site documents a request-quota error code,
		// so every non-auth code stays the generic parse error.
		return nil, native.APIEnvelopeError("torznab", apiErr, d.apikey, 0)
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
		Details:     native.TrimComments(it.Comments),
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
		Grabs:                native.ParseInt64(strings.TrimSpace(it.Grabs)),
		Files:                it.files(),
		Seeders:              max(it.attrInt("seeders"), 0),
		Leechers:             it.attrInt("leechers"),
		Peers:                max(it.attrInt("peers"), 0),
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
	if n := native.ParseInt64(it.attr("size")); n > 0 {
		return n
	}
	if n := native.ParseInt64(strings.TrimSpace(it.Size)); n > 0 {
		return n
	}
	for i := range it.Enclosures {
		if n := native.ParseInt64(strings.TrimSpace(it.Enclosures[i].Length)); n > 0 {
			return n
		}
	}
	return 0
}

// files returns the release's file count: the torznab:attr "files" when present, else
// the plain <files> child element — Jackett's base ResultFromFeedItem fallback.
func (it *item) files() int64 {
	if n := native.ParseInt64(it.attr("files")); n > 0 {
		return n
	}
	return max(native.ParseInt64(it.Files), 0)
}

// category resolves the release's single category id: the LAST numeric <category>
// child element when any are present, else the FIRST torznab:attr "category" value —
// Jackett's base ResultFromFeedItem rule exactly (`categories.Last(...)` vs
// `attributes.First(...)`) — mapped through the driver's active CategoryMap (the live
// ?t=caps tree when fetched, else the placeholder pass-through table).
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
	if id := native.CanonicalIMDBID(it.attr("imdb")); id != "" {
		return id
	}
	return native.CanonicalIMDBID(it.attr("imdbid"))
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
		// Surfaced rather than silently swallowed (autobrr/harbrr#196): an
		// unparseable pubDate used to vanish into "" and the torznab serializer's
		// now-fallback masked it as age≈0 downstream, with no signal a new feed
		// format had appeared.
		d.Log.Debug().Str("driver", d.Def.ID).Str("value", v).Err(err).
			Msg("torznab: pubDate parse failed")
		return ""
	}
	return out
}

// attr returns the value of the first torznab:attr with the given name
// (case-insensitive on name, namespace-matched on the attr element). A missing attr
// yields "".
func (it *item) attr(name string) string {
	return native.AttrValue(it.Attrs, torznabAttrNS, name)
}

// attrInt returns the first torznab:attr with the given name parsed as int64 (0 when
// absent or unparseable).
func (it *item) attrInt(name string) int64 {
	return native.ParseInt64(it.attr(name))
}
