package passthepopcorn

import (
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/normalizer"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
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
	TotalResults flexString `json:"TotalResults"`
	Movies       []ptpMovie `json:"Movies"`
	Page         string     `json:"Page"`
}

// ptpMovie is one movie group. CategoryId (1-6) drives the release's newznab category
// (all map to Movies); Year/ImdbId are JSON strings; Tags become the Genre list; Cover
// is the poster URL. Torrents is the nested torrent list flattened one release each.
type ptpMovie struct {
	GroupID    string       `json:"GroupId"`
	CategoryID string       `json:"CategoryId"`
	Title      string       `json:"Title"`
	Year       flexString   `json:"Year"`
	Cover      string       `json:"Cover"`
	Tags       []string     `json:"Tags"`
	ImdbID     flexString   `json:"ImdbId"`
	Torrents   []ptpTorrent `json:"Torrents"`
}

// ptpTorrent is one torrent row. Id is polymorphic (int OR string), so flexInt tolerates
// either. Size/Snatched/Seeders/Leechers are JSON strings (flexString). FreeleechType is
// a nullable string driving the volume factors; Checked/GoldenPopcorn are flags.
type ptpTorrent struct {
	ID            flexInt    `json:"Id"`
	Quality       string     `json:"Quality"`
	Source        string     `json:"Source"`
	Container     string     `json:"Container"`
	Codec         string     `json:"Codec"`
	Resolution    string     `json:"Resolution"`
	Scene         bool       `json:"Scene"`
	Size          flexString `json:"Size"`
	UploadTime    string     `json:"UploadTime"`
	Snatched      flexString `json:"Snatched"`
	Seeders       flexString `json:"Seeders"`
	Leechers      flexString `json:"Leechers"`
	ReleaseName   string     `json:"ReleaseName"`
	Checked       bool       `json:"Checked"`
	GoldenPopcorn bool       `json:"GoldenPopcorn"`
	FreeleechType *string    `json:"FreeleechType"`
	RemasterTitle *string    `json:"RemasterTitle"`
}

// flexString unmarshals a JSON string OR number into a string. PTP wire-encodes
// TotalResults/Year/ImdbId/Size/Snatched/Seeders/Leechers as JSON STRINGS, but a bare
// number is tolerated too so a strict struct decode never rejects the body.
type flexString string

func (s *flexString) UnmarshalJSON(b []byte) error {
	if len(b) == 0 || string(b) == "null" {
		*s = ""
		return nil
	}
	if b[0] == '"' {
		var str string
		if err := json.Unmarshal(b, &str); err != nil {
			return fmt.Errorf("passthepopcorn: decode string field: %w", err)
		}
		*s = flexString(str)
		return nil
	}
	*s = flexString(b) // a bare JSON number: keep its literal text
	return nil
}

// str returns the trimmed string value.
func (s flexString) str() string { return strings.TrimSpace(string(s)) }

// int64 parses the flexString as a base-10 int64; a blank or unparseable value yields 0
// (a malformed numeric must not fail the whole page — it degrades to 0).
func (s flexString) int64() int64 {
	n, err := strconv.ParseInt(s.str(), 10, 64)
	if err != nil {
		return 0
	}
	return n
}

// flexInt unmarshals a JSON string OR number into an int64. PTP's Torrent.Id is
// polymorphic (int in some rows, string in others — autobrr's pkg/ptp has a custom
// UnmarshalJSON switching float64/string), so both wire forms are tolerated.
type flexInt int64

func (n *flexInt) UnmarshalJSON(b []byte) error {
	if len(b) == 0 || string(b) == "null" {
		*n = 0
		return nil
	}
	s := string(b)
	if b[0] == '"' {
		var str string
		if err := json.Unmarshal(b, &str); err != nil {
			return fmt.Errorf("passthepopcorn: decode numeric field: %w", err)
		}
		s = strings.TrimSpace(str)
	}
	if s == "" {
		*n = 0
		return nil
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		*n = 0
		return nil
	}
	*n = flexInt(v)
	return nil
}

// int64 returns the decoded value as a plain int64.
func (n flexInt) int64() int64 { return int64(n) }

// parseReleases decodes a torrents.php?action=advanced body into normalized releases. A
// non-JSON or malformed body is a parse error. A TotalResults of "0"/blank or a null
// Movies list is an empty page (no error), matching Prowlarr's early return. On a
// populated body it flattens every movie group × each nested torrent into one release and
// sorts by PublishDate descending for a deterministic feed.
func (d *driver) parseReleases(body []byte) ([]*normalizer.Release, error) {
	var resp ptpResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("passthepopcorn: decode search response: %w", search.ErrParseError)
	}
	if resp.TotalResults.str() == "" || resp.TotalResults.int64() == 0 || resp.Movies == nil {
		return nil, nil
	}

	var rels []*normalizer.Release
	for i := range resp.Movies {
		rels = append(rels, d.flattenMovie(&resp.Movies[i])...)
	}
	sortReleases(rels)
	return rels, nil
}

// flattenMovie turns one movie group into one release per nested torrent (group ×
// torrent), the shared movie-group fields (category, year, imdb, genre, poster) copied
// onto every release.
func (d *driver) flattenMovie(m *ptpMovie) []*normalizer.Release {
	rels := make([]*normalizer.Release, 0, len(m.Torrents))
	for i := range m.Torrents {
		rels = append(rels, d.toRelease(m, &m.Torrents[i]))
	}
	return rels
}

// toRelease maps one movie-group × torrent pair to a release. Title is the torrent's
// ReleaseName VERBATIM (Prowlarr does no composition for PTP). Link is the secret-free
// download URL (torrents.php?action=download&id=<id>); the ApiUser/ApiKey headers are
// re-attached server-side at grab time. PublishDate is UploadTime parsed as UTC.
func (d *driver) toRelease(m *ptpMovie, t *ptpTorrent) *normalizer.Release {
	seeders := t.Seeders.int64()
	leechers := t.Leechers.int64()
	return &normalizer.Release{
		Title:                t.ReleaseName,
		Link:                 d.downloadLink(t.ID.int64()),
		Details:              d.infoURL(m.GroupID, t.ID.int64()),
		Categories:           d.categories(m.CategoryID),
		Size:                 t.Size.int64(),
		Grabs:                t.Snatched.int64(),
		Seeders:              seeders,
		Leechers:             leechers,
		Peers:                seeders + leechers,
		PublishDate:          publishDate(t.UploadTime),
		IMDBID:               formatIMDB(m.ImdbID.str()),
		Year:                 m.Year.int64(),
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
	return fmt.Sprintf("%storrents.php?action=download&id=%d", d.baseURL, torrentID)
}

// infoURL builds the human details URL (torrents.php?id=<groupId>&torrentid=<id>),
// mirroring Prowlarr PassThePopcornParser.GetInfoUrl.
func (d *driver) infoURL(groupID string, torrentID int64) string {
	return fmt.Sprintf("%storrents.php?id=%s&torrentid=%d", d.baseURL, url.QueryEscape(groupID), torrentID)
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
	if mapped := canonical(d.caps.CategoryMap.MapTrackerCatToNewznab(id)); mapped != nil {
		return mapped
	}
	return canonical(d.caps.CategoryMap.MapTrackerCatToNewznab(defaultCatID))
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

// sortReleases orders releases by PublishDate descending to mirror a deterministic feed.
// PublishDate is UTC RFC3339, which sorts lexically in chronological order; the stable
// sort preserves group/torrent input order on any tie (equal timestamps).
func sortReleases(rels []*normalizer.Release) {
	sort.SliceStable(rels, func(i, j int) bool {
		return rels[i].PublishDate > rels[j].PublishDate
	})
}
