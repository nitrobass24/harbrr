package filelist

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

const (
	// downloadPath / detailsPath are the Prowlarr-style URLs the parser rebuilds
	// (FileListParser.GetDownloadUrl / GetInfoUrl) rather than trusting an API link.
	downloadPath = "download.php"
	detailsPath  = "details.php"
	// minimumSeedTimeSeconds is the fixed 48h MinimumSeedTime Prowlarr sets for every
	// FileList release; minimumRatio is the fixed 1.
	minimumSeedTimeSeconds = 172800
	minimumRatio           = 1
)

// filelistTorrent mirrors the FileList api.php JSON row (FileListTorrent). The JSON
// is decoded case-insensitively (Go's encoding/json matches field names without case),
// so the PascalCase tags below match the lowercase keys the API emits.
type filelistTorrent struct {
	ID             uint64 `json:"id"`
	Name           string `json:"name"`
	Size           int64  `json:"size"`
	Leechers       int64  `json:"leechers"`
	Seeders        int64  `json:"seeders"`
	TimesCompleted int64  `json:"times_completed"`
	Files          int64  `json:"files"`
	ImdbID         string `json:"imdb"`
	// FileList sends these flags as integers (0/1), not booleans.
	Internal         int64  `json:"internal"`
	FreeLeech        int64  `json:"freeleech"`
	DoubleUp         int64  `json:"doubleup"`
	UploadDate       string `json:"upload_date"`
	Category         string `json:"category"`
	SmallDescription string `json:"small_description"`
}

// filelistError is the error envelope FileList returns ({"error":"…"}); the parser
// surfaces it as a parse error (Prowlarr throws an IndexerException on it).
type filelistError struct {
	Error string `json:"error"`
}

// parseReleases decodes an api.php body (a JSON array of torrents) into normalized
// releases, reproducing FileListParser: title=name, peers=leechers+seeders, the
// freeleech/doubleup volume factors, the description, the imdb id, the
// MapTrackerCatDescToNewznab category, and the +0300 upload_date → UTC publish date,
// with the download URL rebuilt as download.php?id&passkey. When freeleech_only is
// set, a non-freeleech row is dropped (Prowlarr's parser-side filter). A malformed
// body or a {"error":…} envelope is a parse error.
func (d *driver) parseReleases(body []byte) ([]*normalizer.Release, error) {
	if env, ok := decodeError(body); ok {
		return nil, fmt.Errorf("filelist: api error: %s: %w", scrubPasskey(env, d.Cfg), search.ErrParseError)
	}
	var rows []filelistTorrent
	if err := json.Unmarshal(body, &rows); err != nil {
		return nil, fmt.Errorf("filelist: decode search response: %s: %w", apphttp.DecodeErrorDetail(err, body), search.ErrParseError)
	}

	freeOnly := freeleechOnly(d.Cfg)
	releases := make([]*normalizer.Release, 0, len(rows))
	for i := range rows {
		if freeOnly && rows[i].FreeLeech == 0 {
			continue
		}
		rel, err := d.toRelease(&rows[i])
		if err != nil {
			return nil, err
		}
		releases = append(releases, rel)
	}
	native.TraceReleases(d.Log, d.Def.ID, releases)
	return releases, nil
}

// decodeError reports whether the body is a FileList {"error":…} envelope (Prowlarr
// checks for a leading {"error" before deserializing the list) and returns its message.
func decodeError(body []byte) (string, bool) {
	if !strings.HasPrefix(strings.TrimSpace(string(body)), `{"error"`) {
		return "", false
	}
	var env filelistError
	if json.Unmarshal(body, &env) != nil {
		return "unexpected error response", true
	}
	return env.Error, true
}

// toRelease maps one DTO row to a normalized release. Link is the rebuilt
// download.php URL (the served feed routes it through /dl because NeedsResolver=true,
// so the passkey it carries never reaches the feed); Details is the details.php page.
func (d *driver) toRelease(row *filelistTorrent) (*normalizer.Release, error) {
	published, err := parsePublishDate(row.UploadDate)
	if err != nil {
		return nil, err
	}
	rel := &normalizer.Release{
		Title:                row.Name,
		Description:          row.SmallDescription,
		Link:                 d.downloadURL(row.ID),
		Details:              d.detailsURL(row.ID),
		Categories:           d.Caps.CategoryMap.MapTrackerCatDescToNewznab(row.Category),
		Size:                 row.Size,
		Files:                row.Files,
		Grabs:                row.TimesCompleted,
		Seeders:              row.Seeders,
		Leechers:             row.Leechers,
		Peers:                row.Leechers + row.Seeders,
		PublishDate:          published.Format(time.RFC3339),
		DownloadVolumeFactor: volumeFactor(row.FreeLeech, 0, 1),
		UploadVolumeFactor:   volumeFactor(row.DoubleUp, 2, 1),
		MinimumRatio:         minimumRatio,
		MinimumSeedTime:      minimumSeedTimeSeconds,
		IMDBID:               normalizeIMDBID(row.ImdbID),
	}
	return rel, nil
}

// downloadURL rebuilds the Prowlarr download URL: {base}download.php?id={id}&passkey=
// {passkey}. The passkey is a secret; this URL is served only through /dl (the proxy
// keeps it out of the feed) and is never logged.
func (d *driver) downloadURL(id uint64) string {
	params := url.Values{}
	params.Set("id", uintToString(id))
	params.Set("passkey", strings.TrimSpace(d.Cfg["passkey"]))
	return d.BaseURL + downloadPath + "?" + params.Encode()
}

// detailsURL rebuilds the Prowlarr info URL: {base}details.php?id={id}.
func (d *driver) detailsURL(id uint64) string {
	params := url.Values{}
	params.Set("id", uintToString(id))
	return d.BaseURL + detailsPath + "?" + params.Encode()
}

// parsePublishDate reproduces Prowlarr's DateTime.Parse(upload_date + " +0300",
// AdjustToUniversal): the upload_date is a "yyyy-MM-dd HH:mm:ss" string in EET
// (+0300), normalized to UTC. An unparseable value is a parse error (Prowlarr throws).
func parsePublishDate(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	t, err := time.Parse("2006-01-02 15:04:05 -0700", s+" +0300")
	if err != nil {
		return time.Time{}, fmt.Errorf("filelist: unparseable upload_date %q: %w", s, search.ErrParseError)
	}
	return t.UTC(), nil
}

// normalizeIMDBID reproduces Prowlarr's imdb handling — TrimStart('t') then parse to
// an int — but re-renders it as the canonical "tt"+7-digit form harbrr stores (the
// same FullImdbId shape used in the request), so a row's `imdb` of "tt0133093" or
// "133093" both normalize to "tt0133093". A value of two characters or fewer (or a
// non-numeric one) yields "" (Prowlarr leaves ImdbId 0).
func normalizeIMDBID(raw string) string {
	if len(strings.TrimSpace(raw)) <= 2 {
		return ""
	}
	return fullIMDBID(raw)
}

// volumeFactor returns whenTrue when the integer flag is set (FileList sends 0/1),
// else whenFalse (the freeleech → DownloadVolumeFactor 0/1 and doubleup →
// UploadVolumeFactor 2/1 rules).
func volumeFactor(flag int64, whenTrue, whenFalse float64) float64 {
	if flag != 0 {
		return whenTrue
	}
	return whenFalse
}

func uintToString(n uint64) string {
	return strconv.FormatUint(n, 10)
}
