package avistaz

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/mapper"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/normalizer"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
)

// avistazResponse / avistazRelease mirror the api/v1/jackett/torrents JSON (the
// subset harbrr consumes). Numeric stats are pointers so an absent field is 0, not a
// decode error. Category is the ExoticaZ-only response-category dict (the base parser
// ignores it; the ExoticaZ variant reads its keys).
type avistazResponse struct {
	// Data is a pointer so a 2xx body that carries no `data` array (a `{}`, `null`,
	// or maintenance/proxy stub) is distinguishable from a legitimate empty result
	// (`{"data":[]}`): the former is a parse error, the latter is zero releases.
	Data *[]avistazRelease `json:"data"`
}

type avistazRelease struct {
	URL          string            `json:"url"`
	Download     string            `json:"download"`
	Category     map[string]string `json:"category"`
	MovieTV      *avistazIDInfo    `json:"movie_tv"`
	CreatedAtISO string            `json:"created_at_iso"`
	FileName     string            `json:"file_name"`
	InfoHash     string            `json:"info_hash"`
	Leech        *int64            `json:"leech"`
	Completed    *int64            `json:"completed"`
	Seed         *int64            `json:"seed"`
	FileSize     *int64            `json:"file_size"`
	FileCount    *int64            `json:"file_count"`
	DownloadMul  *float64          `json:"download_multiply"`
	UploadMul    *float64          `json:"upload_multiply"`
	VideoQuality string            `json:"video_quality"`
	Type         string            `json:"type"`
}

type avistazIDInfo struct {
	Tmdb string `json:"tmdb"`
	Tvdb string `json:"tvdb"`
	Imdb string `json:"imdb"`
}

// parseReleases decodes a 2xx api/v1/jackett/torrents body into normalized releases,
// reproducing AvistazParserBase: title=file_name, peers=leech+seed, the volume
// multipliers, the size-based MinimumSeedTime, the movie_tv ids, and the
// type+video_quality category — sorted by publish date descending. The status has
// already been handled by Search (404/429/non-2xx). A malformed body, an unparseable
// date, or an unrecognized category type is a parse error (Prowlarr throws).
func (d *driver) parseReleases(body []byte) ([]*normalizer.Release, error) {
	var resp avistazResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("avistaz: decode search response: %w", search.ErrParseError)
	}
	if resp.Data == nil {
		return nil, fmt.Errorf("avistaz: search response carried no data array: %w", search.ErrParseError)
	}
	data := *resp.Data
	releases := make([]*normalizer.Release, 0, len(data))
	for i := range data {
		rel, err := d.toRelease(&data[i])
		if err != nil {
			return nil, err
		}
		releases = append(releases, rel)
	}
	// PublishDate is uniform RFC3339 UTC, so a lexical descending sort is
	// chronological; SliceStable mirrors .NET's stable OrderByDescending.
	sort.SliceStable(releases, func(i, j int) bool {
		return releases[i].PublishDate > releases[j].PublishDate
	})
	return releases, nil
}

// toRelease maps one DTO row to a normalized release. Link is the download URL (the
// served feed routes it through /dl because NeedsResolver=true); Details is the info
// page. The volume factors default to 1 (full cost) when absent; a freeleech torrent
// carries an explicit 0 multiplier.
func (d *driver) toRelease(row *avistazRelease) (*normalizer.Release, error) {
	cats, err := d.parseCategories(row)
	if err != nil {
		return nil, err
	}
	published, err := parsePublishDate(row.CreatedAtISO)
	if err != nil {
		return nil, err
	}
	rel := &normalizer.Release{
		Title:                row.FileName,
		Link:                 row.Download,
		Details:              row.URL,
		InfoHash:             row.InfoHash,
		Categories:           cats,
		Size:                 deref(row.FileSize),
		Files:                deref(row.FileCount),
		Grabs:                deref(row.Completed),
		Seeders:              deref(row.Seed),
		Leechers:             deref(row.Leech),
		Peers:                deref(row.Leech) + deref(row.Seed),
		PublishDate:          published.Format(time.RFC3339),
		DownloadVolumeFactor: derefFloat(row.DownloadMul, 1),
		UploadVolumeFactor:   derefFloat(row.UploadMul, 1),
		MinimumRatio:         1,
		MinimumSeedTime:      minimumSeedTime(deref(row.FileSize)),
	}
	if row.MovieTV != nil {
		rel.IMDBID = fullIMDBID(row.MovieTV.Imdb)
		rel.TMDBID = coerceInt(row.MovieTV.Tmdb)
		rel.TVDBID = coerceInt(row.MovieTV.Tvdb)
	}
	return rel, nil
}

// parseCategories derives the release's newznab category. The base mapping uses
// type+video_quality (AvistazParserBase); the ExoticaZ variant (response-category
// dict) is added with that site.
func (d *driver) parseCategories(row *avistazRelease) ([]int, error) {
	return baseCategories(row)
}

// baseCategories reproduces AvistazParserBase.ParseCategories: MOVIE/TV-SHOW map to
// the HD/UHD/SD variant by resolution, MUSIC maps to Audio; any other type is a parse
// error (Prowlarr throws).
func baseCategories(row *avistazRelease) ([]int, error) {
	name, err := categoryName(row.Type, row.VideoQuality)
	if err != nil {
		return nil, err
	}
	cat, ok := mapper.GetByName(name)
	if !ok {
		return nil, fmt.Errorf("avistaz: no newznab category for %q: %w", name, search.ErrParseError)
	}
	return []int{cat.ID}, nil
}

// categoryName maps the release type and resolution to a standard category name.
func categoryName(typ, videoQuality string) (string, error) {
	switch strings.ToUpper(strings.TrimSpace(typ)) {
	case "MOVIE":
		return "Movies/" + resolutionSuffix(videoQuality), nil
	case "TV-SHOW":
		return "TV/" + resolutionSuffix(videoQuality), nil
	case "MUSIC":
		return "Audio", nil
	default:
		return "", fmt.Errorf("avistaz: unrecognized release type %q: %w", typ, search.ErrParseError)
	}
}

// resolutionSuffix maps a video_quality to the HD/UHD/SD category suffix, matching
// Prowlarr's hd set {1080p,1080i,720p} (checked before 2160p->UHD, else SD).
func resolutionSuffix(videoQuality string) string {
	switch videoQuality {
	case "1080p", "1080i", "720p":
		return "HD"
	case "2160p":
		return "UHD"
	default:
		return "SD"
	}
}

// minimumSeedTime reproduces the size-based MinimumSeedTime: 72h by default, else a
// size-scaled value (>50 GiB uses the log curve; otherwise 259200 + GiB*7200). The
// integer truncation order matches the C# casts.
func minimumSeedTime(fileSize int64) int64 {
	if fileSize <= 0 {
		return 259200
	}
	gib := float64(fileSize) / (1024 * 1024 * 1024)
	if gib > 50.0 {
		return int64((100*math.Log(gib))-219.2023) * 3600
	}
	return 259200 + int64(gib*7200)
}

// parsePublishDate parses the created_at_iso ISO-8601 string and normalizes it to UTC
// (Prowlarr's DateTime.Parse with AdjustToUniversal). The common ISO layouts are
// tried; an unparseable value is a parse error.
func parsePublishDate(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02T15:04:05", "2006-01-02 15:04:05"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("avistaz: unparseable created_at_iso %q: %w", s, search.ErrParseError)
}

// coerceInt parses a numeric string id to int64, returning 0 when empty or non-numeric
// (matching ParseUtil.TryCoerceInt's GetValueOrDefault(0)).
func coerceInt(s string) int64 {
	n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	if err != nil {
		return 0
	}
	return n
}

func deref(p *int64) int64 {
	if p == nil {
		return 0
	}
	return *p
}

func derefFloat(p *float64, def float64) float64 {
	if p == nil {
		return def
	}
	return *p
}
