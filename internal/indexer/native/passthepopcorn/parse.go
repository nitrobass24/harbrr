package passthepopcorn

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	apphttp "github.com/autobrr/harbrr/internal/http"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/normalizer"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
	"github.com/autobrr/harbrr/internal/indexer/native"
)

// customCatCutoff bounds the canonical newznab id range. The caps map carries a
// description on every entry, so the mapper synthesises a 1:1 custom category
// (id + CustomCategoryOffset = 100000); the parser keeps only the canonical id and
// discards that synthetic one (mirroring gazelle/broadcastthenet).
const customCatCutoff = 100000

// defaultCatID is the tracker category id used when a movie-group's CategoryId is
// blank: PTP is movie-only and every CategoryId 1-6 maps to Movies, so an absent id
// degrades to "1" (Feature Film -> Movies 2000) rather than no category.
const defaultCatID = "1"

// minSeedTime is Prowlarr's fixed PTP MinimumSeedTime (345600s = 4 days); MinimumRatio
// is fixed at 1. Both are constant per release in PassThePopcornParser.
const (
	minSeedTime  = 345600
	minimumRatio = 1
)

// uploadTimeLayout is PTP's UploadTime wire format ("YYYY-MM-DD HH:MM:SS"), which
// Prowlarr parses as UTC (UploadTime + " +0000"). A parsed value renders to UTC RFC3339.
const uploadTimeLayout = "2006-01-02 15:04:05"

// ptpResponse is the torrents.php?action=advanced JSON envelope. TotalResults is a JSON
// string ("0"/blank/missing => empty page); Movies is the movie-group list (null =>
// empty). Page is unused. AuthKey/PassKey appear at this level in the live JSON but are
// empty strings in the feed (download auth is via headers), so they are not modelled.
type ptpResponse struct {
	TotalResults native.FlexString `json:"TotalResults"`
	Movies       []ptpMovie        `json:"Movies"`
	Page         string            `json:"Page"`
}

// ptpMovie is one movie group. CategoryId (1-6) drives the release's newznab category
// (all map to Movies); Year/ImdbId are JSON strings; Tags become the Genre list; Cover
// is the poster URL. Torrents is the nested torrent list flattened one release each.
type ptpMovie struct {
	GroupID    string            `json:"GroupId"`
	CategoryID string            `json:"CategoryId"`
	Title      string            `json:"Title"`
	Year       native.FlexString `json:"Year"`
	Cover      string            `json:"Cover"`
	Tags       []string          `json:"Tags"`
	ImdbID     native.FlexString `json:"ImdbId"`
	Torrents   []ptpTorrent      `json:"Torrents"`
}

// ptpTorrent is one torrent row. Id is polymorphic (int OR string), so native.FlexInt tolerates
// either. Size/Snatched/Seeders/Leechers are JSON strings (native.FlexString). FreeleechType is
// a nullable string driving the volume factors; Checked/GoldenPopcorn are flags.
type ptpTorrent struct {
	ID            native.FlexInt    `json:"Id"`
	Quality       string            `json:"Quality"`
	Source        string            `json:"Source"`
	Container     string            `json:"Container"`
	Codec         string            `json:"Codec"`
	Resolution    string            `json:"Resolution"`
	Scene         bool              `json:"Scene"`
	Size          native.FlexString `json:"Size"`
	UploadTime    string            `json:"UploadTime"`
	Snatched      native.FlexString `json:"Snatched"`
	Seeders       native.FlexString `json:"Seeders"`
	Leechers      native.FlexString `json:"Leechers"`
	ReleaseName   string            `json:"ReleaseName"`
	Checked       bool              `json:"Checked"`
	GoldenPopcorn bool              `json:"GoldenPopcorn"`
	FreeleechType *string           `json:"FreeleechType"`
	RemasterTitle *string           `json:"RemasterTitle"`
}

// parseReleases decodes a torrents.php?action=advanced body into normalized releases. A
// non-JSON or malformed body is a parse error. A TotalResults of "0"/blank or a null
// Movies list is an empty page (no error), matching Prowlarr's early return. On a
// populated body it flattens every movie group × each nested torrent into one release and
// sorts by PublishDate descending for a deterministic feed.
func (d *driver) parseReleases(body []byte) ([]*normalizer.Release, error) {
	var resp ptpResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("passthepopcorn: decode search response: %s: %w", apphttp.DecodeErrorDetail(err, body), search.ErrParseError)
	}
	if resp.TotalResults.Str() == "" || resp.TotalResults.Int64() == 0 || resp.Movies == nil {
		return nil, nil
	}

	var rels []*normalizer.Release
	for i := range resp.Movies {
		rels = append(rels, d.flattenMovie(&resp.Movies[i])...)
	}
	native.SortByPublishDateDesc(rels)
	native.TraceReleases(d.Log, d.Def.ID, rels)
	return rels, nil
}

// flattenMovie turns one movie group into one release per nested torrent (group ×
// torrent), the shared movie-group fields (category, year, imdb, genre, poster) copied
// onto every release.
func (d *driver) flattenMovie(m *ptpMovie) []*normalizer.Release {
	rels := make([]*normalizer.Release, 0, len(m.Torrents))
	for i := range m.Torrents {
		// A torrent whose Id decoded to 0 (empty/malformed) would yield a broken
		// download link (action=download&id=0); skip the row rather than emit it.
		if m.Torrents[i].ID.Int64() == 0 {
			continue
		}
		rels = append(rels, d.toRelease(m, &m.Torrents[i]))
	}
	return rels
}

// toRelease maps one movie-group × torrent pair to a release. Title is the torrent's
// ReleaseName VERBATIM (Prowlarr does no composition for PTP). Link is the secret-free
// download URL (torrents.php?action=download&id=<id>); the ApiUser/ApiKey headers are
// re-attached server-side at grab time. PublishDate is UploadTime parsed as UTC.
func (d *driver) toRelease(m *ptpMovie, t *ptpTorrent) *normalizer.Release {
	seeders := t.Seeders.Int64()
	leechers := t.Leechers.Int64()
	return &normalizer.Release{
		Title:                t.ReleaseName,
		Link:                 d.downloadLink(t.ID.Int64()),
		Details:              d.infoURL(m.GroupID, t.ID.Int64()),
		Categories:           d.categories(m.CategoryID),
		Size:                 t.Size.Int64(),
		Grabs:                t.Snatched.Int64(),
		Seeders:              seeders,
		Leechers:             leechers,
		Peers:                seeders + leechers,
		PublishDate:          publishDate(t.UploadTime),
		IMDBID:               formatIMDB(m.ImdbID.Str()),
		Year:                 m.Year.Int64(),
		Genre:                strings.Join(m.Tags, ", "),
		Poster:               posterURL(m.Cover),
		DownloadVolumeFactor: downloadVolumeFactor(t.FreeleechType),
		UploadVolumeFactor:   uploadVolumeFactor(t.FreeleechType),
		MinimumRatio:         minimumRatio,
		MinimumSeedTime:      minSeedTime,
	}
}

// downloadLink builds the secret-free PTP download URL. The torrent id is the only
// query param; auth is re-attached via the ApiUser/ApiKey headers at grab time, so the
// served feed link carries no secret (Prowlarr PassThePopcornParser.GetDownloadUrl).
func (d *driver) downloadLink(torrentID int64) string {
	return fmt.Sprintf("%storrents.php?action=download&id=%d", d.BaseURL, torrentID)
}

// infoURL builds the human details URL (torrents.php?id=<groupId>&torrentid=<id>),
// mirroring Prowlarr PassThePopcornParser.GetInfoUrl.
func (d *driver) infoURL(groupID string, torrentID int64) string {
	return fmt.Sprintf("%storrents.php?id=%s&torrentid=%d", d.BaseURL, url.QueryEscape(groupID), torrentID)
}

// categories maps the movie-group CategoryId (1-6) to its newznab category through the
// caps id map, keeping only the canonical id and discarding the mapper's synthesised 1:1
// custom id. A blank/unmapped CategoryId defaults to Feature Film -> Movies (2000),
// matching PTP's movie-only catalogue (Prowlarr MapTrackerCatToNewznab(result.CategoryId)).
func (d *driver) categories(categoryID string) []int {
	id := strings.TrimSpace(categoryID)
	if id == "" {
		id = defaultCatID
	}
	if mapped := canonical(d.Caps.CategoryMap.MapTrackerCatToNewznab(id)); mapped != nil {
		return mapped
	}
	return canonical(d.Caps.CategoryMap.MapTrackerCatToNewznab(defaultCatID))
}

// canonical keeps only the canonical newznab category id, dropping the mapper's
// synthesised 1:1 custom id (>= 100000), so each release carries exactly one category.
func canonical(ids []int) []int {
	for _, id := range ids {
		if id < customCatCutoff {
			return []int{id}
		}
	}
	return nil
}

// downloadVolumeFactor maps PTP's FreeleechType to the download volume factor, matching
// Prowlarr: Freeleech/Neutral Leech -> 0 (free), Half Leech -> 0.5, anything else -> 1.
func downloadVolumeFactor(freeleechType *string) float64 {
	switch freeleechUpper(freeleechType) {
	case "FREELEECH", "NEUTRAL LEECH":
		return 0
	case "HALF LEECH":
		return 0.5
	default:
		return 1
	}
}

// uploadVolumeFactor maps PTP's FreeleechType to the upload volume factor, matching
// Prowlarr: only Neutral Leech -> 0 (no upload counted); everything else -> 1.
func uploadVolumeFactor(freeleechType *string) float64 {
	if freeleechUpper(freeleechType) == "NEUTRAL LEECH" {
		return 0
	}
	return 1
}

// freeleechUpper normalises a nullable FreeleechType to its upper-cased trimmed value
// (matching Prowlarr's torrent.FreeleechType?.ToUpperInvariant()); a null type yields "".
func freeleechUpper(freeleechType *string) string {
	if freeleechType == nil {
		return ""
	}
	return strings.ToUpper(strings.TrimSpace(*freeleechType))
}

// publishDate renders PTP's UploadTime ("YYYY-MM-DD HH:MM:SS") as UTC RFC3339. Prowlarr
// parses it as UTC (UploadTime + " +0000"); an empty or unparseable value yields "".
func publishDate(uploadTime string) string {
	s := strings.TrimSpace(uploadTime)
	if s == "" {
		return ""
	}
	t, err := time.ParseInLocation(uploadTimeLayout, s, time.UTC)
	if err != nil {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

// formatIMDB renders PTP's digits-only ImdbId ("0081229") as the canonical "tt"+7-digit
// feed form, matching the normalizer. A blank or non-numeric id yields "" (omitted).
func formatIMDB(imdbID string) string {
	if imdbID == "" {
		return ""
	}
	n, err := strconv.ParseInt(imdbID, 10, 64)
	if err != nil || n == 0 {
		return ""
	}
	return fmt.Sprintf("tt%07d", n)
}

// posterURL returns the movie Cover only when it is an absolute http(s) URL, mirroring
// Prowlarr's GetPosterUrl (which rejects a non-absolute or non-http(s) cover).
func posterURL(cover string) string {
	c := strings.TrimSpace(cover)
	if c == "" {
		return ""
	}
	u, err := url.Parse(c)
	if err != nil || !u.IsAbs() || (u.Scheme != "http" && u.Scheme != "https") {
		return ""
	}
	return c
}
