package speedapp

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	apphttp "github.com/autobrr/harbrr/internal/http"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/normalizer"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
	"github.com/autobrr/harbrr/internal/indexer/native"
)

const (
	customCategoryOffset = 100000
	minimumRatio         = 1
	minimumSeedTime      = 432000
)

type apiCategory struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

type apiRow struct {
	ID                   int64       `json:"id"`
	URL                  string      `json:"url"`
	Name                 string      `json:"name"`
	ShortDescription     string      `json:"short_description"`
	Poster               string      `json:"poster"`
	Size                 int64       `json:"size"`
	CreatedAt            string      `json:"created_at"`
	TimesCompleted       int64       `json:"times_completed"`
	Seeders              int64       `json:"seeders"`
	Leechers             int64       `json:"leechers"`
	DownloadVolumeFactor float64     `json:"download_volume_factor"`
	UploadVolumeFactor   float64     `json:"upload_volume_factor"`
	IMDBID               string      `json:"imdb_id"`
	Category             apiCategory `json:"category"`
}

func (d *driver) parseReleases(body []byte) ([]*normalizer.Release, error) {
	var rows *[]apiRow
	if err := json.Unmarshal(body, &rows); err != nil {
		return nil, fmt.Errorf("speedapp: decode search response: %s: %w", apphttp.DecodeErrorDetail(err, body), search.ErrParseError)
	}
	if rows == nil {
		return nil, fmt.Errorf("speedapp: unrecognized search response: %w", search.ErrParseError)
	}
	releases := make([]*normalizer.Release, 0, len(*rows))
	for i := range *rows {
		release, err := d.toRelease(&(*rows)[i])
		if err != nil {
			return nil, err
		}
		releases = append(releases, release)
	}
	return releases, nil
}

func (d *driver) toRelease(row *apiRow) (*normalizer.Release, error) {
	published, err := native.PublishDate(row.CreatedAt, d.Clock)
	if err != nil {
		return nil, fmt.Errorf("speedapp: unparseable created_at %q: %w: %w", strings.TrimSpace(row.CreatedAt), search.ErrParseError, err)
	}
	return &normalizer.Release{
		Title:                cleanTitle(row.Name),
		Description:          row.ShortDescription,
		Details:              row.URL,
		GUID:                 row.URL,
		Link:                 d.BaseURL + "api/torrent/" + strconv.FormatInt(row.ID, 10) + "/download",
		Categories:           d.categories(row.Category.ID),
		Size:                 row.Size,
		PublishDate:          published,
		Grabs:                row.TimesCompleted,
		Seeders:              row.Seeders,
		Leechers:             row.Leechers,
		Peers:                row.Seeders + row.Leechers,
		DownloadVolumeFactor: row.DownloadVolumeFactor,
		UploadVolumeFactor:   row.UploadVolumeFactor,
		MinimumRatio:         minimumRatio,
		MinimumSeedTime:      minimumSeedTime,
		IMDBID:               native.CanonicalIMDBID(row.IMDBID),
		Poster:               row.Poster,
	}, nil
}

var requestTitle = regexp.MustCompile(`(?i)\[REQUEST(?:ED)?\]`)

func cleanTitle(title string) string {
	return strings.Trim(requestTitle.ReplaceAllString(title, ""), " .")
}

func (d *driver) categories(id int64) []int {
	for _, category := range d.Caps.CategoryMap.MapTrackerCatToNewznab(strconv.FormatInt(id, 10)) {
		if category < customCategoryOffset {
			return []int{category}
		}
	}
	return nil
}
