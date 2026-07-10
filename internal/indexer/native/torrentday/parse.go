package torrentday

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	apphttp "github.com/autobrr/harbrr/internal/http"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/normalizer"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
	"github.com/autobrr/harbrr/internal/indexer/native"
)

const (
	// downloadPath / detailsPath are the Prowlarr-style URLs the parser rebuilds
	// (TorrentDayParser): the download URL uses the torrent id for BOTH path segments
	// (download.php/<id>/<id>.torrent), the details URL is details.php?id=<id>.
	downloadPath = "download.php"
	detailsPath  = "details.php"
	// minimumSeedTimeSeconds is Prowlarr's fixed TorrentDay MinimumSeedTime (72h);
	// minimumRatio is the fixed 1. These are NOT the IPTorrents constants — TorrentDay
	// sets its own.
	minimumSeedTimeSeconds = 259200
	minimumRatio           = 1
)

// torrentDayRow is one row of the /t.json flat array (TorrentDayParser's per-torrent
// access). Every numeric field is decoded through flexInt / flexFloat because the wire
// form is either a JSON number or a JSON string — Prowlarr's dynamic cast tolerates
// both, so a strict struct decode must too. `c` (the category id) is read via
// `.ToString()` in Prowlarr, so it too may arrive as a number or string.
type torrentDayRow struct {
	ID                 flexInt   `json:"t"`
	Name               string    `json:"name"`
	CTime              flexInt   `json:"ctime"`
	Size               flexInt   `json:"size"`
	Files              flexInt   `json:"files"`
	Completed          flexInt   `json:"completed"`
	Seeders            flexInt   `json:"seeders"`
	Leechers           flexInt   `json:"leechers"`
	Category           flexInt   `json:"c"`
	ImdbID             string    `json:"imdb-id"`
	DownloadMultiplier flexFloat `json:"download-multiplier"`
}

// parseReleases decodes a /t.json body (a FLAT JSON array of torrents) into normalized
// releases, reproducing TorrentDayParser: title=name verbatim, peers=seeders+leechers,
// the download-multiplier-derived DownloadVolumeFactor, the imdb id, the `c`->newznab
// category, and the unix ctime -> UTC publish date, with the download URL rebuilt as
// download.php/<id>/<id>.torrent. When freeleech_only is set, a non-freeleech row
// (download-multiplier != 0) is dropped. An empty body ([]) yields zero releases; a body
// that is not a JSON array (a login-redirect HTML page, a truncated response) is a parse
// error. Releases are sorted by torrent id for a deterministic feed.
func (d *driver) parseReleases(body []byte) ([]*normalizer.Release, error) {
	if !isJSONArray(body) {
		return nil, fmt.Errorf("torrentday: search response is not a JSON array: %w", search.ErrParseError)
	}
	var rows []torrentDayRow
	if err := json.Unmarshal(body, &rows); err != nil {
		return nil, fmt.Errorf("torrentday: decode search response: %s: %w", apphttp.DecodeErrorDetail(err, body), search.ErrParseError)
	}

	freeOnly := freeleechOnly(d.cfg)
	releases := make([]*normalizer.Release, 0, len(rows))
	for i := range rows {
		if freeOnly && rows[i].DownloadMultiplier.float64WithDefault(1) != 0 {
			continue
		}
		releases = append(releases, d.toRelease(&rows[i]))
	}
	sort.SliceStable(releases, func(i, j int) bool {
		return releases[i].Link < releases[j].Link
	})
	native.TraceReleases(d.log, d.def.ID, releases)
	return releases, nil
}

// isJSONArray reports whether body's first non-space byte is '[' (TorrentDay always
// returns a JSON array; anything else — an HTML login page from a redirect, an error
// stub — is not a result page and is treated as a parse error rather than decoded).
func isJSONArray(body []byte) bool {
	trimmed := strings.TrimSpace(string(body))
	return strings.HasPrefix(trimmed, "[")
}

// toRelease maps one /t.json row to a normalized release. Link is the rebuilt
// download.php/<id>/<id>.torrent URL (served only through /dl because
// NeedsResolver=true, so the session cookie never reaches the feed); Details is the
// details.php page; Categories come from the `c` id through the caps; Peers is
// seeders+leechers; PublishDate is the unix ctime rendered as UTC RFC3339; and the
// DownloadVolumeFactor is the download-multiplier (default 1, 0 = freeleech).
func (d *driver) toRelease(row *torrentDayRow) *normalizer.Release {
	seeders := row.Seeders.int64()
	leechers := row.Leechers.int64()
	return &normalizer.Release{
		Title:                row.Name,
		Link:                 d.downloadURL(row.ID.int64()),
		Details:              d.detailsURL(row.ID.int64()),
		Categories:           d.categories(row.Category),
		Size:                 row.Size.int64(),
		Files:                row.Files.int64(),
		Grabs:                row.Completed.int64(),
		Seeders:              seeders,
		Leechers:             leechers,
		Peers:                seeders + leechers,
		PublishDate:          time.Unix(row.CTime.int64(), 0).UTC().Format(time.RFC3339),
		DownloadVolumeFactor: row.DownloadMultiplier.float64WithDefault(1),
		UploadVolumeFactor:   1,
		MinimumRatio:         minimumRatio,
		MinimumSeedTime:      minimumSeedTimeSeconds,
		IMDBID:               normalizeIMDBID(row.ImdbID),
	}
}

// categories maps a row's tracker category id (`c`) to its newznab category through the
// caps, keeping only the canonical newznab id and discarding the mapper's synthesised
// 1:1 custom id (those ids are >= customCatCutoff). An unmapped id yields no category
// (an uncategorised release) rather than failing the page.
func (d *driver) categories(c flexInt) []int {
	for _, id := range d.caps.CategoryMap.MapTrackerCatToNewznab(c.string()) {
		if id < customCatCutoff {
			return []int{id}
		}
	}
	return nil
}

// downloadURL rebuilds the Prowlarr download URL: {base}download.php/<id>/<id>.torrent.
// The torrent id is used for BOTH path segments (Prowlarr/Jackett build it this way, NOT
// the release name). The session cookie this URL needs rides as a request header
// server-side via /dl (NeedsResolver=true), never in the URL, so the feed carries no
// secret.
func (d *driver) downloadURL(id int64) string {
	idStr := strconv.FormatInt(id, 10)
	return d.baseURL + downloadPath + "/" + idStr + "/" + idStr + ".torrent"
}

// detailsURL rebuilds the Prowlarr info URL: {base}details.php?id=<id>.
func (d *driver) detailsURL(id int64) string {
	return d.baseURL + detailsPath + "?id=" + strconv.FormatInt(id, 10)
}

// normalizeIMDBID re-renders a row's imdb-id as harbrr's canonical "tt"+7-digit form
// (matching filelist/iptorrents): "tt1234567" and "1234567" both normalize to
// "tt1234567". A value of two characters or fewer, or a non-numeric one, yields "".
func normalizeIMDBID(raw string) string {
	if len(strings.TrimSpace(raw)) <= 2 {
		return ""
	}
	return fullIMDBID(raw)
}

// fullIMDBID renders an imdb id as Prowlarr's FullImdbId ("tt" + the numeric id,
// zero-padded to seven digits). A leading "tt" is stripped, the rest parsed; a
// non-numeric or empty id yields "".
func fullIMDBID(raw string) string {
	s := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(raw)), "tt")
	if s == "" {
		return ""
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return ""
	}
	return fmt.Sprintf("tt%07d", n)
}

// freeleechOnly reports whether the freeleech_only checkbox is enabled. harbrr stores a
// checked checkbox as Jackett's "True" sentinel; common truthy spellings are accepted so
// whatever the management API persists is interpreted consistently (mirrors iptorrents/
// filelist).
func freeleechOnly(cfg map[string]string) bool {
	switch strings.ToLower(strings.TrimSpace(cfg["freeleech_only"])) {
	case "true", "1", "on", "yes":
		return true
	default:
		return false
	}
}

// flexInt unmarshals a JSON number OR a JSON string into an int64-bearing value.
// TorrentDay wire-encodes numerics inconsistently (sometimes a bare number, sometimes a
// quoted string), so every numeric row field uses flexInt and a strict struct decode
// never rejects the body (mirrors broadcastthenet flexString).
type flexInt string

func (f *flexInt) UnmarshalJSON(b []byte) error {
	if len(b) == 0 || string(b) == "null" {
		*f = ""
		return nil
	}
	if b[0] == '"' {
		var str string
		if err := json.Unmarshal(b, &str); err != nil {
			return fmt.Errorf("torrentday: decode numeric string field: %w", err)
		}
		*f = flexInt(str)
		return nil
	}
	*f = flexInt(b) // a bare JSON number: keep its literal text
	return nil
}

// int64 parses the flexInt as a base-10 int64; a blank or unparseable value yields 0 (a
// malformed numeric must degrade to 0, not fail the whole page).
func (f flexInt) int64() int64 {
	n, err := strconv.ParseInt(strings.TrimSpace(string(f)), 10, 64)
	if err != nil {
		return 0
	}
	return n
}

// string returns the flexInt's literal text (used for the category id, which the caps
// map matches as a string).
func (f flexInt) string() string { return strings.TrimSpace(string(f)) }

// flexFloat unmarshals a JSON number OR a JSON string into a float64-bearing value. The
// download-multiplier is a `double?` in Prowlarr (default 1 when absent), and may arrive
// as either a number or a string.
type flexFloat string

func (f *flexFloat) UnmarshalJSON(b []byte) error {
	if len(b) == 0 || string(b) == "null" {
		*f = ""
		return nil
	}
	if b[0] == '"' {
		var str string
		if err := json.Unmarshal(b, &str); err != nil {
			return fmt.Errorf("torrentday: decode numeric string field: %w", err)
		}
		*f = flexFloat(str)
		return nil
	}
	*f = flexFloat(b)
	return nil
}

// float64WithDefault parses the flexFloat as a float64; a blank (absent) field yields
// def (the download-multiplier defaults to 1 when the row omits it). An unparseable
// non-blank value also degrades to def.
func (f flexFloat) float64WithDefault(def float64) float64 {
	s := strings.TrimSpace(string(f))
	if s == "" {
		return def
	}
	n, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return def
	}
	return n
}

// customCatCutoff bounds the canonical newznab id range: the mapper synthesises a 1:1
// custom category per tracker id with an id >= this cutoff; the parser discards it so
// each release carries exactly one canonical category (mirrors broadcastthenet).
const customCatCutoff = 100000
