package nebulance

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	apphttp "github.com/autobrr/harbrr/internal/http"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/login"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/normalizer"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
	"github.com/autobrr/harbrr/internal/indexer/native"
)

const customCategoryOffset = 100000

type apiResponse struct {
	CurrentPage  int64     `json:"current_page"`
	TotalPages   int64     `json:"total_pages"`
	Count        int64     `json:"count"`
	TotalResults int64     `json:"total_results"`
	Items        *[]apiRow `json:"items"`
	Error        *apiError `json:"error"`
}

type apiError struct {
	Message string `json:"message"`
}

type apiRow struct {
	ReleaseTitle string   `json:"rls_name"`
	Category     string   `json:"cat"`
	Size         int64    `json:"size"`
	Seed         int64    `json:"seed"`
	Leech        int64    `json:"leech"`
	Snatch       int64    `json:"snatch"`
	DownloadLink string   `json:"download"`
	Episode      int64    `json:"episode"`
	FileList     []string `json:"file_list"`
	GroupName    string   `json:"group_name"`
	TorrentID    int64    `json:"group_id"`
	IMDBID       string   `json:"imdb_id"`
	Season       int64    `json:"season"`
	Series       string   `json:"series"`
	SeriesID     int64    `json:"series_id"`
	TVMazeID     int64    `json:"tvmaze_id"`
	PublishUTC   string   `json:"rls_utc"`
	Tags         []string `json:"tags"`
}

// parseReleases decodes an NBL response into normalized releases. Structured and
// bare authentication failures retain [login.ErrLoginFailed]; malformed or
// unrecognized responses retain [search.ErrParseError].
func (d *driver) parseReleases(body []byte) ([]*normalizer.Release, error) {
	var response apiResponse
	if err := json.Unmarshal(body, &response); err != nil {
		if isBareAuthError(body) {
			return nil, fmt.Errorf("nebulance: invalid API key: %w", login.ErrLoginFailed)
		}
		if containsFold(body, "api is down") {
			return nil, fmt.Errorf("nebulance: API is unavailable: %w", search.ErrParseError)
		}
		return nil, fmt.Errorf("nebulance: decode search response: %s: %w", apphttp.DecodeErrorDetail(err, body), search.ErrParseError)
	}
	if response.Error != nil {
		return nil, d.apiError(response.Error.Message)
	}
	if response.Items == nil {
		return nil, fmt.Errorf("nebulance: unrecognized search response: %w", search.ErrParseError)
	}
	items := *response.Items
	if response.TotalResults == 0 || len(items) == 0 {
		return []*normalizer.Release{}, nil
	}

	releases := make([]*normalizer.Release, 0, len(items))
	for i := range items {
		release, err := d.toRelease(&items[i])
		if err != nil {
			return nil, err
		}
		releases = append(releases, release)
	}
	native.TraceReleases(d.Log, d.Def.ID, releases)
	return releases, nil
}

func (d *driver) apiError(message string) error {
	message = d.Scrub(strings.TrimSpace(message))
	if looksLikeAuthError(message) {
		return fmt.Errorf("nebulance: API error: %s: %w", message, login.ErrLoginFailed)
	}
	return fmt.Errorf("nebulance: API error: %s: %w", message, search.ErrParseError)
}

func looksLikeAuthError(message string) bool {
	lower := strings.ToLower(message)
	for _, marker := range []string{"api key", "apikey", "credential", "unauthorized", "authentication"} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return strings.Contains(lower, "invalid key")
}

func (d *driver) toRelease(row *apiRow) (*normalizer.Release, error) {
	publishDate, err := parsePublishDate(row.PublishUTC)
	if err != nil {
		return nil, fmt.Errorf("nebulance: parse publish date: %w: %w", err, search.ErrParseError)
	}
	title := strings.TrimSpace(row.ReleaseTitle)
	if title == "" {
		title = strings.TrimSpace(row.GroupName)
	}
	details := d.BaseURL + "torrents.php?id=" + strconv.FormatInt(row.TorrentID, 10)
	seedTime := int64(86400)
	if strings.EqualFold(strings.TrimSpace(row.Category), "season") {
		seedTime = 432000
	}
	return &normalizer.Release{
		Title:                title,
		Details:              details,
		GUID:                 apphttp.RedactURLIdentity(details),
		Link:                 strings.TrimSpace(row.DownloadLink),
		Categories:           d.categories(row.ReleaseTitle),
		Size:                 row.Size,
		Files:                int64(len(row.FileList)),
		PublishDate:          publishDate,
		Grabs:                row.Snatch,
		Seeders:              row.Seed,
		Leechers:             row.Leech,
		Peers:                row.Seed + row.Leech,
		MinimumSeedTime:      seedTime,
		DownloadVolumeFactor: 0,
		UploadVolumeFactor:   1,
		IMDBID:               normalizeIMDBID(row.IMDBID),
		TVMazeID:             positiveInt64(row.TVMazeID),
	}, nil
}

func (d *driver) categories(title string) []int {
	trackerCategory := qualityCategory(title)
	for _, category := range d.Caps.CategoryMap.MapTrackerCatToNewznab(trackerCategory) {
		if category < customCategoryOffset {
			return []int{category}
		}
	}
	return []int{5000}
}

var (
	webDLSource   = regexp.MustCompile(`(?i)\b(?:WEB[-_. ]DL|WEBDL|WebRip|iTunesHD|WebHD)\b`)
	hdtvSource    = regexp.MustCompile(`(?i)\bHDTV\b`)
	bluraySource  = regexp.MustCompile(`(?i)\b(?:BluRay|Blu-Ray|HDDVD|BD)\b`)
	bdripSource   = regexp.MustCompile(`(?i)\bBDRip\b`)
	brripSource   = regexp.MustCompile(`(?i)\bBRRip\b`)
	dvdSource     = regexp.MustCompile(`(?i)\b(?:DVD|DVDRip|NTSC|PAL|xvidvd)\b`)
	sdSource      = regexp.MustCompile(`(?i)\b(?:WS[-_. ]DSR|DSR|PDTV|SDTV|TVRip)\b`)
	rawHDSource   = regexp.MustCompile(`(?i)\b(?:TrollHD|RawHD|1080i[-_. ]HDTV|Raw[-_. ]HD|MPEG[-_. ]?2)\b`)
	resolution4K  = regexp.MustCompile(`(?i)\b2160p\b`)
	resolutionHD  = regexp.MustCompile(`(?i)\b(?:720p|1280x720|1080p|1920x1080)\b`)
	resolution480 = regexp.MustCompile(`(?i)\b(?:480p|640x480|848x480)\b`)
	resolution576 = regexp.MustCompile(`(?i)\b576p\b`)
	xvidCodec     = regexp.MustCompile(`(?i)\b(?:Xvid|divx)\b`)
	highDefPDTV   = regexp.MustCompile(`(?i)hr[-_. ]ws`)
)

// qualityCategory ports Prowlarr's TvCategoryFromQualityParser decision order.
func qualityCategory(title string) string {
	normalized := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(title), "_", " "))
	switch {
	case webDLSource.MatchString(normalized):
		return resolutionCategory(normalized, false)
	case hdtvSource.MatchString(normalized):
		category := resolutionCategory(normalized, false)
		if category == "1" {
			return "2"
		}
		return category
	case bluraySource.MatchString(normalized), bdripSource.MatchString(normalized), brripSource.MatchString(normalized):
		if xvidCodec.MatchString(normalized) {
			return "2"
		}
		return resolutionCategory(normalized, true)
	case dvdSource.MatchString(normalized):
		return "2"
	case sdSource.MatchString(normalized):
		if highDefPDTV.MatchString(normalized) {
			return "3"
		}
		return "2"
	case rawHDSource.MatchString(normalized):
		return "3"
	default:
		return "1"
	}
}

func resolutionCategory(title string, include576p bool) string {
	switch {
	case resolution4K.MatchString(title):
		return "4"
	case resolutionHD.MatchString(title):
		return "3"
	case resolution480.MatchString(title), include576p && resolution576.MatchString(title):
		return "2"
	default:
		return "1"
	}
}

var publishDateLayouts = []string{
	time.RFC3339Nano,
	time.RFC3339,
	"2006-01-02T15:04:05-0700",
	"2006-01-02T15:04:05",
	"2006-01-02 15:04:05",
}

func parsePublishDate(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	for _, layout := range publishDateLayouts {
		if parsed, err := time.Parse(layout, raw); err == nil {
			return parsed.UTC().Format(time.RFC3339), nil
		}
	}
	return "", fmt.Errorf("unsupported date value %q", raw)
}

func positiveInt64(value int64) int64 {
	if value > 0 {
		return value
	}
	return 0
}

// isBareAuthError recognizes only NBL's complete plain-text authentication
// responses, preventing matching marker text inside malformed HTML or help pages.
func isBareAuthError(body []byte) bool {
	message := strings.ToLower(strings.TrimSpace(string(body)))
	return message == "invalid params" || message == "invalid api key"
}

func containsFold(body []byte, marker string) bool {
	return strings.Contains(strings.ToLower(string(body)), marker)
}
