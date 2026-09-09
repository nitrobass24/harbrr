package torrentday

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
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
// access). Every numeric field is decoded through native.FlexString because the wire
// form is either a JSON number or a JSON string — Prowlarr's dynamic cast tolerates
// both, so a strict struct decode must too. `c` (the category id) is read via
// `.ToString()` in Prowlarr, so it too may arrive as a number or string.
type torrentDayRow struct {
	ID                 native.FlexString `json:"t"`
	Name               string            `json:"name"`
	CTime              native.FlexString `json:"ctime"`
	Size               native.FlexString `json:"size"`
	Files              native.FlexString `json:"files"`
	Completed          native.FlexString `json:"completed"`
	Seeders            native.FlexString `json:"seeders"`
	Leechers           native.FlexString `json:"leechers"`
	Category           native.FlexString `json:"c"`
	ImdbID             string            `json:"imdb-id"`
	DownloadMultiplier native.FlexString `json:"download-multiplier"`
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
	// TorrentDay always returns a JSON array; anything else — an HTML login page from
	// a redirect, an error stub — is not a result page and is a parse error, not a
	// decode failure.
	if !bytes.HasPrefix(bytes.TrimSpace(body), []byte{'['}) {
		return nil, fmt.Errorf("torrentday: search response is not a JSON array: %w", search.ErrParseError)
	}
	var rows []torrentDayRow
	if err := json.Unmarshal(body, &rows); err != nil {
		return nil, fmt.Errorf("torrentday: decode search response: %s: %w", apphttp.DecodeErrorDetail(err, body), search.ErrParseError)
	}

	freeOnly := freeleechOnly(d.Cfg)
	releases := make([]*normalizer.Release, 0, len(rows))
	for i := range rows {
		if freeOnly && float64WithDefault(rows[i].DownloadMultiplier, 1) != 0 {
			continue
		}
		releases = append(releases, d.toRelease(&rows[i]))
	}
	sort.SliceStable(releases, func(i, j int) bool {
		return releases[i].Link < releases[j].Link
	})
	native.TraceReleases(d.Log, d.Def.ID, releases)
	return releases, nil
}

// toRelease maps one /t.json row to a normalized release. Link is the rebuilt
// download.php/<id>/<id>.torrent URL (served only through /dl because
// NeedsResolver=true, so the session cookie never reaches the feed); Details is the
// details.php page; Categories come from the `c` id through the caps; Peers is
// seeders+leechers; PublishDate is the unix ctime rendered as UTC RFC3339; and the
// DownloadVolumeFactor is the download-multiplier (default 1, 0 = freeleech).
func (d *driver) toRelease(row *torrentDayRow) *normalizer.Release {
	seeders := row.Seeders.Int64()
	leechers := row.Leechers.Int64()
	return &normalizer.Release{
		Title:                row.Name,
		Link:                 d.downloadURL(row.ID.Int64()),
		Details:              d.detailsURL(row.ID.Int64()),
		Categories:           d.categories(row.Category),
		Size:                 row.Size.Int64(),
		Files:                row.Files.Int64(),
		Grabs:                row.Completed.Int64(),
		Seeders:              seeders,
		Leechers:             leechers,
		Peers:                seeders + leechers,
		PublishDate:          time.Unix(row.CTime.Int64(), 0).UTC().Format(time.RFC3339),
		DownloadVolumeFactor: float64WithDefault(row.DownloadMultiplier, 1),
		UploadVolumeFactor:   1,
		MinimumRatio:         minimumRatio,
		MinimumSeedTime:      minimumSeedTimeSeconds,
		IMDBID:               native.CanonicalIMDBID(row.ImdbID),
	}
}

// categories maps a row's tracker category id (`c`) to its newznab category through the
// caps, keeping only the canonical newznab id and discarding the mapper's synthesised
// 1:1 custom id (native.FirstStandardCat). An unmapped id yields no category
// (an uncategorised release) rather than failing the page.
func (d *driver) categories(c native.FlexString) []int {
	return native.FirstStandardCat(d.Caps.CategoryMap.MapTrackerCatToNewznab(c.Str()))
}

// downloadURL rebuilds the Prowlarr download URL: {base}download.php/<id>/<id>.torrent.
// The torrent id is used for BOTH path segments (Prowlarr/Jackett build it this way, NOT
// the release name). The session cookie this URL needs rides as a request header
// server-side via /dl (NeedsResolver=true), never in the URL, so the feed carries no
// secret.
func (d *driver) downloadURL(id int64) string {
	idStr := strconv.FormatInt(id, 10)
	return d.BaseURL + downloadPath + "/" + idStr + "/" + idStr + ".torrent"
}

// detailsURL rebuilds the Prowlarr info URL: {base}details.php?id=<id>.
func (d *driver) detailsURL(id int64) string {
	return d.BaseURL + detailsPath + "?id=" + strconv.FormatInt(id, 10)
}

// freeleechOnly reports whether the freeleech_only checkbox is enabled.
func freeleechOnly(cfg map[string]string) bool {
	return native.CheckboxOn(cfg["freeleech_only"])
}

// float64WithDefault parses f as a float64; a blank (absent) field yields def — the
// download-multiplier is a `double?` in Prowlarr, defaulting to 1 when the row omits it.
// An unparseable non-blank value also degrades to def.
func float64WithDefault(f native.FlexString, def float64) float64 {
	s := f.Str()
	if s == "" {
		return def
	}
	n, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return def
	}
	return n
}
