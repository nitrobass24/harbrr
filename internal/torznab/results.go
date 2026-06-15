package torznab

import (
	"encoding/xml"
	"strconv"
	"strings"
	"time"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/normalizer"
)

// Torznab/Atom namespace URIs. harbrr controls the prefixes (atom:, torznab:)
// explicitly by using literal prefixed element/attribute names rather than
// encoding/xml's namespace machinery (which cannot pin a prefix), so the served
// feed matches what Jackett's ResultPage emits and *arr parses.
const (
	atomNamespace    = "http://www.w3.org/2005/Atom"
	torznabNamespace = "http://torznab.com/schemas/2015/feed"
	enclosureType    = "application/x-bittorrent"
	feedLanguage     = "en-US"  // ChannelInfo default
	feedCategory     = "search" // ChannelInfo default
)

// FeedInfo carries the indexer identity and feed metadata the results document
// needs. It is sourced from the loaded definition (Name/Description/SiteLink/
// Type/ID) by the caller; SelfURL is the atom:link self href and MUST NOT carry
// query secrets (the caller builds it from the request scheme/host/path only).
type FeedInfo struct {
	IndexerID   string
	Name        string
	Description string
	SiteLink    string
	Type        string
	SelfURL     string
}

type rssFeed struct {
	XMLName      xml.Name   `xml:"rss"`
	Version      string     `xml:"version,attr"`
	XMLNSAtom    string     `xml:"xmlns:atom,attr"`
	XMLNSTorznab string     `xml:"xmlns:torznab,attr"`
	Channel      rssChannel `xml:"channel"`
}

type rssChannel struct {
	AtomLink    atomLink  `xml:"atom:link"`
	Title       string    `xml:"title"`
	Description string    `xml:"description"`
	Link        string    `xml:"link"`
	Language    string    `xml:"language"`
	Category    string    `xml:"category"`
	Items       []rssItem `xml:"item"`
}

type atomLink struct {
	Href string `xml:"href,attr"`
	Rel  string `xml:"rel,attr"`
	Type string `xml:"type,attr"`
}

// rssItem mirrors ResultPage.ToXml's <item> element order exactly. Plain RSS
// <category> elements precede <enclosure>; the torznab:attr block (category
// attrs first, then everything else) follows it.
type rssItem struct {
	Title       string         `xml:"title"`
	GUID        string         `xml:"guid"`
	Indexer     jackettIndexer `xml:"jackettindexer"`
	Type        string         `xml:"type"`
	Comments    string         `xml:"comments,omitempty"`
	PubDate     string         `xml:"pubDate"`
	Size        int64          `xml:"size"`
	Files       *int64         `xml:"files,omitempty"`
	Grabs       *int64         `xml:"grabs,omitempty"`
	Description string         `xml:"description"`
	Link        string         `xml:"link"`
	Categories  []int          `xml:"category"`
	Enclosure   enclosure      `xml:"enclosure"`
	Attrs       []torznabAttr
}

type jackettIndexer struct {
	ID   string `xml:"id,attr"`
	Name string `xml:",chardata"`
}

type enclosure struct {
	URL    string `xml:"url,attr"`
	Length int64  `xml:"length,attr"`
	Type   string `xml:"type,attr"`
}

type torznabAttr struct {
	XMLName xml.Name `xml:"torznab:attr"`
	Name    string   `xml:"name,attr"`
	Value   string   `xml:"value,attr"`
}

// AcquisitionRewriter optionally replaces a release's served <link>/<enclosure>
// URL and its <guid>. It receives the default acquisition link (the resolved link,
// else the magnet) and returns the replacement link, a replacement guid, and
// ok=true to apply them; ok=false keeps the defaults. The Torznab handler uses it to
// route a resolver-needing indexer's links through the /dl proxy so a passkey-bearing
// link never reaches the feed, while keeping a stable, passkey-free guid.
type AcquisitionRewriter func(acquisitionLink string) (link, guid string, ok bool)

// MarshalResults renders the Torznab results feed (t=search and the typed search
// modes) for an indexer's releases. now supplies the pubDate fallback for a
// release without a date (Jackett uses DateTime.Now). An empty release slice
// renders a valid feed with a full <channel> header and zero <item>s. The
// serializer is pure: it renders exactly the releases given, in order; guid
// de-duplication (Jackett's behavior) is the caller's responsibility via GUIDFor.
func MarshalResults(feed FeedInfo, releases []*normalizer.Release, now time.Time) ([]byte, error) {
	return MarshalResultsRewritten(feed, releases, now, nil)
}

// MarshalResultsRewritten is MarshalResults with an optional per-release acquisition
// rewriter (the /dl proxy). A nil rewriter serves links and guids unchanged.
func MarshalResultsRewritten(feed FeedInfo, releases []*normalizer.Release, now time.Time, rewrite AcquisitionRewriter) ([]byte, error) {
	items := make([]rssItem, 0, len(releases))
	for _, r := range releases {
		if r == nil { // boundary guard: the *arr feed must never panic on a stray nil
			continue
		}
		items = append(items, buildItem(feed, r, now, rewrite))
	}
	doc := rssFeed{
		Version:      "2.0",
		XMLNSAtom:    atomNamespace,
		XMLNSTorznab: torznabNamespace,
		Channel: rssChannel{
			AtomLink:    atomLink{Href: feed.SelfURL, Rel: "self", Type: "application/rss+xml"},
			Title:       sanitizeXMLText(feed.Name),
			Description: sanitizeXMLText(feed.Description),
			Link:        feed.SiteLink,
			Language:    feedLanguage,
			Category:    feedCategory,
			Items:       items,
		},
	}
	return marshalDocument("rss", doc)
}

// GUIDFor derives a release's RSS guid using Jackett's FixResults precedence:
// the download Link, else the Magnet, else the Details page. harbrr's normalizer
// requires at least one acquisition link, so a guid is always available. Exposed
// so the request handler can de-duplicate releases by guid (Jackett's
// post-FixResults GroupBy) using the same derivation the serializer emits.
func GUIDFor(r *normalizer.Release) string {
	switch {
	case r.Link != "":
		return r.Link
	case r.Magnet != "":
		return r.Magnet
	default:
		return r.Details
	}
}

// acquisitionLink is the <link>/<enclosure url> value: the download link, else
// the magnet (Jackett: Link?.AbsoluteUri ?? MagnetUri?.AbsoluteUri ?? "").
func acquisitionLink(r *normalizer.Release) string {
	if r.Link != "" {
		return r.Link
	}
	return r.Magnet
}

// buildItem assembles one <item> from a normalized release. When rewrite is
// non-nil and applies, the served <link>/<enclosure> and <guid> are replaced (the
// /dl proxy), otherwise the resolved link/magnet and GUIDFor are served directly.
func buildItem(feed FeedInfo, r *normalizer.Release, now time.Time, rewrite AcquisitionRewriter) rssItem {
	link := acquisitionLink(r)
	guid := GUIDFor(r)
	if rewrite != nil {
		if newLink, newGUID, ok := rewrite(link); ok {
			link, guid = newLink, newGUID
		}
	}
	item := rssItem{
		Title:       sanitizeXMLText(r.Title),
		GUID:        guid,
		Indexer:     jackettIndexer{ID: feed.IndexerID, Name: sanitizeXMLText(feed.Name)},
		Type:        feed.Type,
		Comments:    r.Details,
		PubDate:     formatPubDate(r.PublishDate, now),
		Size:        r.Size,
		Files:       positiveOrNil(r.Files),
		Grabs:       positiveOrNil(r.Grabs),
		Description: sanitizeXMLText(r.Description),
		Link:        link,
		Categories:  r.Categories,
		Enclosure:   enclosure{URL: link, Length: r.Size, Type: enclosureType},
	}
	item.Attrs = buildAttrs(r)
	return item
}

// buildAttrs builds the torznab:attr block in Jackett's exact order: category
// attrs, then external ids, then media fields, then torrent stats/factors.
func buildAttrs(r *normalizer.Release) []torznabAttr {
	attrs := make([]torznabAttr, 0, 16)
	for _, c := range r.Categories {
		attrs = appendAttr(attrs, "category", strconv.Itoa(c))
	}
	attrs = appendExternalIDAttrs(attrs, r)
	attrs = appendMediaAttrs(attrs, r)
	attrs = appendTorrentAttrs(attrs, r)
	return attrs
}

// appendExternalIDAttrs emits rageid, tvdbid, imdb (7-digit, no "tt"), imdbid
// ("tt"+7-digit), tmdbid, tvmazeid, traktid, doubanid — each only when present.
func appendExternalIDAttrs(attrs []torznabAttr, r *normalizer.Release) []torznabAttr {
	attrs = appendIntAttr(attrs, "rageid", r.RageID)
	attrs = appendIntAttr(attrs, "tvdbid", r.TVDBID)
	if r.IMDBID != "" {
		attrs = appendAttr(attrs, "imdb", strings.TrimPrefix(r.IMDBID, "tt"))
		attrs = appendAttr(attrs, "imdbid", r.IMDBID)
	}
	attrs = appendIntAttr(attrs, "tmdbid", r.TMDBID)
	attrs = appendIntAttr(attrs, "tvmazeid", r.TVMazeID)
	attrs = appendIntAttr(attrs, "traktid", r.TraktID)
	attrs = appendIntAttr(attrs, "doubanid", r.DoubanID)
	return attrs
}

// appendMediaAttrs emits genre, year and the book/music descriptive fields,
// each only when present (matching Jackett's null guard).
func appendMediaAttrs(attrs []torznabAttr, r *normalizer.Release) []torznabAttr {
	attrs = appendStringAttr(attrs, "genre", wireGenre(r.Genre))
	attrs = appendIntAttr(attrs, "year", r.Year)
	attrs = appendStringAttr(attrs, "author", sanitizeXMLText(r.Author))
	attrs = appendStringAttr(attrs, "booktitle", sanitizeXMLText(r.BookTitle))
	attrs = appendStringAttr(attrs, "publisher", sanitizeXMLText(r.Publisher))
	attrs = appendStringAttr(attrs, "artist", sanitizeXMLText(r.Artist))
	attrs = appendStringAttr(attrs, "album", sanitizeXMLText(r.Album))
	attrs = appendStringAttr(attrs, "label", sanitizeXMLText(r.Label))
	attrs = appendStringAttr(attrs, "track", sanitizeXMLText(r.Track))
	return attrs
}

// appendTorrentAttrs emits seeders/peers (always — required, non-nullable in
// harbrr), coverurl/infohash/magneturl (when present), the minimum ratio/seed
// time (when present), and the volume factors (always — harbrr makes the 1.0
// default explicit, a deliberate divergence recorded in testdata/README.md).
func appendTorrentAttrs(attrs []torznabAttr, r *normalizer.Release) []torznabAttr {
	attrs = appendAttr(attrs, "seeders", strconv.FormatInt(r.Seeders, 10))
	attrs = appendAttr(attrs, "peers", strconv.FormatInt(r.Peers, 10))
	attrs = appendStringAttr(attrs, "coverurl", r.Poster)
	attrs = appendStringAttr(attrs, "infohash", sanitizeXMLText(r.InfoHash))
	attrs = appendStringAttr(attrs, "magneturl", r.Magnet)
	if r.MinimumRatio > 0 {
		attrs = appendAttr(attrs, "minimumratio", formatFloat(r.MinimumRatio))
	}
	attrs = appendIntAttr(attrs, "minimumseedtime", r.MinimumSeedTime)
	attrs = appendAttr(attrs, "downloadvolumefactor", formatFloat(r.DownloadVolumeFactor))
	attrs = appendAttr(attrs, "uploadvolumefactor", formatFloat(r.UploadVolumeFactor))
	return attrs
}

func appendAttr(attrs []torznabAttr, name, value string) []torznabAttr {
	return append(attrs, torznabAttr{Name: name, Value: value})
}

// appendIntAttr emits an integer attr only when the value is positive (harbrr's
// coerced ids are 0 when absent, so 0 means "not present").
func appendIntAttr(attrs []torznabAttr, name string, value int64) []torznabAttr {
	if value <= 0 {
		return attrs
	}
	return appendAttr(attrs, name, strconv.FormatInt(value, 10))
}

// appendStringAttr emits a string attr only when non-empty.
func appendStringAttr(attrs []torznabAttr, name, value string) []torznabAttr {
	if value == "" {
		return attrs
	}
	return appendAttr(attrs, name, value)
}

// positiveOrNil returns a pointer to v when v > 0, else nil. harbrr cannot
// distinguish an extracted 0 from an absent files/grabs field, so 0 is treated
// as absent (omitted) — recorded as an accepted divergence in testdata/README.md.
func positiveOrNil(v int64) *int64 {
	if v <= 0 {
		return nil
	}
	return &v
}

// formatFloat renders a volume factor / ratio without a trailing decimal for
// integral values ("1", "0.5", "0"), matching harbrr's canonical JSON form.
func formatFloat(v float64) string {
	return strconv.FormatFloat(v, 'f', -1, 64)
}

// wireGenre converts harbrr's normalized genre (comma-joined — the filter-facing
// form Jackett also uses internally) to the Torznab WIRE form Jackett serializes
// in ResultPage: the genre list joined with ", " (comma+space). harbrr's
// normalizer guarantees no genre part contains a comma, so this rewrite is
// lossless and reproduces Jackett's string.Join(", ", Genres) exactly.
func wireGenre(genre string) string {
	if genre == "" {
		return ""
	}
	return strings.ReplaceAll(genre, ",", ", ")
}

// formatPubDate renders an RFC3339 publish date as RFC1123Z (Jackett's
// "ddd, dd MMM yyyy HH:mm:ss zzz" form, e.g. "Sat, 14 Mar 2015 17:10:42 -0400").
// An empty or unparseable date falls back to now (Jackett's DateTime.Now for a
// DateTime.MinValue date). A future date is clamped to now, reproducing
// BaseIndexer.FixResults' clamp (harbrr always clamps; Jackett only in release
// builds — recorded as a divergence in testdata/README.md).
func formatPubDate(rfc3339 string, now time.Time) string {
	if rfc3339 != "" {
		if t, err := time.Parse(time.RFC3339, rfc3339); err == nil {
			if t.After(now) {
				return now.Format(time.RFC1123Z)
			}
			return t.Format(time.RFC1123Z)
		}
	}
	return now.Format(time.RFC1123Z)
}
