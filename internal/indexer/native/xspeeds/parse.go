package xspeeds

import (
	"bytes"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/normalizer"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
	"github.com/autobrr/harbrr/internal/indexer/native"
)

var addedDate = regexp.MustCompile(`\d{2}-\d{2}-\d{4} \d{2}:\d{2}`)

func (d *driver) parseReleases(body []byte) ([]*normalizer.Release, error) {
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("xspeeds: parse browse HTML: %w: %w", search.ErrParseError, err)
	}
	var releases []*normalizer.Release
	doc.Find(`table#sortabletable > tbody > tr:has(a[href*="details.php?id="])`).Each(func(_ int, row *goquery.Selection) {
		release, ok := d.parseRow(row)
		if ok && (!freeleechOnly(d.Cfg) || release.DownloadVolumeFactor == 0) {
			releases = append(releases, release)
		}
	})
	native.TraceReleases(d.Log, "xspeeds", releases)
	return releases, nil
}

func (d *driver) parseRow(row *goquery.Selection) (*normalizer.Release, bool) {
	detailsNode := row.Find(`div > a[href*="details.php?id="]`).First()
	title := strings.TrimSpace(detailsNode.Text())
	details, detailsOK := d.nodeURL(detailsNode)
	download, downloadOK := d.downloadURL(row.Find(`a[href*="download.php"]`).First())
	if title == "" || !detailsOK || !downloadOK {
		return nil, false
	}

	seeders := parseInteger(cellText(row, 7))
	leechers := parseInteger(cellText(row, 8))
	return &normalizer.Release{
		Title:                title,
		Description:          cleanDescription(row.Find(".tooltip-content > div:nth-of-type(2)").First().Text()),
		Details:              details,
		GUID:                 details,
		Link:                 download,
		Size:                 normalizer.ParseSize(cellText(row, 5)),
		Categories:           d.rowCategories(row),
		Seeders:              seeders,
		Leechers:             leechers,
		Peers:                seeders + leechers,
		Grabs:                parseInteger(cellText(row, 6)),
		PublishDate:          parseDate(row),
		DownloadVolumeFactor: downloadFactor(row),
		UploadVolumeFactor:   uploadFactor(row),
		MinimumRatio:         0.8,
	}, true
}

func (d *driver) nodeURL(node *goquery.Selection) (string, bool) {
	href, exists := node.Attr("href")
	if !exists || strings.TrimSpace(href) == "" {
		return "", false
	}
	resolved, err := resolveURL(d.cookieURL, href)
	if err != nil {
		return "", false
	}
	return resolved, true
}

// downloadURL trusts only the scraped torrent id and rebuilds the full URL against
// the configured base, so a row cannot redirect the authenticated grab to another host.
func (d *driver) downloadURL(node *goquery.Selection) (string, bool) {
	href, exists := node.Attr("href")
	if !exists {
		return "", false
	}
	parsed, err := url.Parse(strings.TrimSpace(href))
	if err != nil {
		return "", false
	}
	id := strings.TrimSpace(parsed.Query().Get("id"))
	if id == "" {
		return "", false
	}
	target := *d.cookieURL
	target.Path = strings.TrimRight(target.Path, "/") + "/download.php"
	target.RawPath = ""
	target.RawQuery = url.Values{"id": {id}}.Encode()
	target.Fragment = ""
	return target.String(), true
}

func resolveURL(base *url.URL, raw string) (string, error) {
	reference, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", errors.New("invalid URL")
	}
	resolved := base.ResolveReference(reference)
	if (resolved.Scheme != "http" && resolved.Scheme != "https") || resolved.Host == "" {
		return "", errors.New("URL is not HTTP(S)")
	}
	if resolved.User != nil {
		return "", errors.New("URL contains userinfo")
	}
	return resolved.String(), nil
}

func resolveSameOriginURL(base *url.URL, raw string) (string, error) {
	resolved, err := resolveURL(base, raw)
	if err != nil {
		return "", err
	}
	parsed, err := url.Parse(resolved)
	if err != nil ||
		!strings.EqualFold(parsed.Scheme, base.Scheme) ||
		!strings.EqualFold(parsed.Host, base.Host) {
		return "", errors.New("URL is not on the configured tracker origin")
	}
	return resolved, nil
}

func (d *driver) rowCategories(row *goquery.Selection) []int {
	href, exists := row.Find("td:nth-of-type(1) a").First().Attr("href")
	if !exists {
		return nil
	}
	parsed, err := url.Parse(href)
	if err != nil {
		return nil
	}
	return canonicalCategories(d.Caps.CategoryMap.MapTrackerCatToNewznab(parsed.Query().Get("category")))
}

func canonicalCategories(categories []int) []int {
	if len(categories) == 0 {
		return nil
	}
	categories = slices.Clone(categories)
	slices.Sort(categories)
	return slices.Compact(categories)
}

func cellText(row *goquery.Selection, number int) string {
	return strings.TrimSpace(row.Find(fmt.Sprintf("td:nth-of-type(%d)", number)).First().Text())
}

func cleanDescription(description string) string {
	description = strings.ReplaceAll(description, "|", ",")
	description = strings.ReplaceAll(description, " ", "")
	return strings.TrimSpace(description)
}

func parseInteger(raw string) int64 {
	var digits strings.Builder
	for _, char := range raw {
		if char >= '0' && char <= '9' {
			digits.WriteRune(char)
		}
	}
	if digits.Len() == 0 {
		return 0
	}
	value, err := strconv.ParseInt(digits.String(), 10, 64)
	if err != nil {
		return 0
	}
	return value
}

// parseDate reads XSpeeds' "added" timestamp. It does NOT go through
// native.PublishDate: the tracker emits a day-first "02-01-2006 15:04", which
// dateparse rejects outright (it reads neither an absolute nor a relative form from
// it), so the layout has to be named explicitly. An unparseable value yields "" —
// the beyondhd/hdbits convention — rather than failing the row.
func parseDate(row *goquery.Selection) string {
	raw := strings.TrimSpace(row.Find("td:nth-of-type(2) > div:last-child").First().Text())
	token := addedDate.FindString(raw)
	if token == "" {
		return ""
	}
	parsed, err := time.ParseInLocation("02-01-2006 15:04", token, time.UTC)
	if err != nil {
		return ""
	}
	return parsed.Format(time.RFC3339)
}

func downloadFactor(row *goquery.Selection) float64 {
	if row.Find(`img[title^="Free Torrent"], img[title^="Sitewide Free Torrent"]`).Length() > 0 {
		return 0
	}
	if row.Find(`img[title^="Silver Torrent"]`).Length() > 0 {
		return 0.5
	}
	return 1
}

func uploadFactor(row *goquery.Selection) float64 {
	if row.Find(`img[title^="x2 Torrent"]`).Length() > 0 {
		return 2
	}
	return 1
}

func freeleechOnly(cfg map[string]string) bool {
	return native.CheckboxOn(cfg["freeleech_only"])
}
