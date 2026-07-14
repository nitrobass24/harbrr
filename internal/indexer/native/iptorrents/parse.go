package iptorrents

import (
	"bytes"
	"fmt"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/normalizer"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
	"github.com/autobrr/harbrr/internal/indexer/native"
)

const (
	// minimumSeedTimeSeconds is Prowlarr's fixed IPTorrents MinimumSeedTime (336h).
	minimumSeedTimeSeconds = 1209600
	// defaultSizeColumn is the fallback size column index Prowlarr uses when the
	// "Sort by size" header is not found (IPTorrents.cs FindColumnIndexOrDefault).
	defaultSizeColumn = 5
)

// columnLayout holds the per-page column indexes resolved from the header row by text.
// IPTorrents reorders columns by site setting, so the parser never hardcodes an index;
// it locates each stat by its "Sort by …" header link, exactly as Prowlarr does.
type columnLayout struct {
	size, files, grabs, seeders, leechers int
}

// parseReleases scrapes the IPTorrents torrent-list HTML into normalized releases,
// reproducing IPTorrentsParser.ParseResponse: rows from `table#torrents > tbody > tr`,
// columns resolved by header text, the relative "time ago" publish date, and the
// `span.free` freeleech flag. A row with no `a.hv` title link is "no results" and
// skipped; a row with no download link is un-grabbable and also skipped. Malformed
// markup (no parseable document, an unparseable date) is a parse error.
func (d *driver) parseReleases(body []byte) ([]*normalizer.Release, error) {
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("iptorrents: parse search HTML: %w", search.ErrParseError)
	}

	layout := resolveColumns(headerColumns(doc), rowCellCount(doc))
	releases := make([]*normalizer.Release, 0)
	var parseErr error
	doc.Find(`table#torrents > tbody > tr`).EachWithBreak(func(_ int, row *goquery.Selection) bool {
		rel, ok, err := d.parseRow(row, layout)
		if err != nil {
			parseErr = err
			return false
		}
		if ok {
			releases = append(releases, rel)
		}
		return true
	})
	if parseErr != nil {
		return nil, parseErr
	}
	native.TraceReleases(d.Log, d.Def.ID, releases)
	return releases, nil
}

// parseRow maps one torrent row to a normalized release. It returns (nil, false, nil)
// for a header/no-results row (no `a.hv`) or a row missing a download link — both are
// skipped, not errors. A row with a title and link but an unparseable date is a parse
// error.
func (d *driver) parseRow(row *goquery.Selection, layout columnLayout) (*normalizer.Release, bool, error) {
	titleLink := row.Find("a.hv").First()
	if titleLink.Length() == 0 {
		return nil, false, nil
	}
	dlHref, ok := row.Find(`a[href^="/download.php/"]`).First().Attr("href")
	if !ok {
		return nil, false, nil
	}

	published, err := d.parsePublishDate(rowPublishText(row))
	if err != nil {
		return nil, false, err
	}

	seeders := cells(row).intAt(layout.seeders)
	leechers := cells(row).intAt(layout.leechers)
	rel := &normalizer.Release{
		Title:                cleanTitle(titleLink.Text()),
		Link:                 d.absoluteURL(dlHref),
		Details:              d.absoluteURL(attrOr(titleLink, "href")),
		Categories:           d.rowCategories(row),
		Size:                 parseSizeBytes(cells(row).textAt(layout.size)),
		Files:                cells(row).intAt(layout.files),
		Grabs:                cells(row).intAt(layout.grabs),
		Seeders:              seeders,
		Leechers:             leechers,
		Peers:                seeders + leechers,
		PublishDate:          published.Format(time.RFC3339),
		DownloadVolumeFactor: freeleechFactor(row),
		UploadVolumeFactor:   1,
		MinimumRatio:         1,
		MinimumSeedTime:      minimumSeedTimeSeconds,
	}
	return rel, true, nil
}

// rowCategories maps the category-icon href to newznab ids. The icon link
// (`td:nth-of-type(1) a[href^="?"]`) carries `?<id>`; the leading '?' is dropped and
// the id mapped through the caps. A row without the icon (the site's "Text"/"Code"
// category-column mode) yields no category — Prowlarr throws, but harbrr degrades to an
// uncategorised release rather than failing the whole page (see the README divergence).
func (d *driver) rowCategories(row *goquery.Selection) []int {
	href, ok := row.Find(`td:nth-of-type(1) a[href^="?"]`).First().Attr("href")
	if !ok {
		return nil
	}
	return d.Caps.CategoryMap.MapTrackerCatToNewznab(strings.TrimPrefix(href, "?"))
}

// rowPublishText extracts the relative-date text from `div.sub`, mirroring Prowlarr:
// split on '|', take the last segment, split that on " by " and take the first part
// (dropping the uploader name). The result is a "time ago" string.
func rowPublishText(row *goquery.Selection) string {
	sub := row.Find("div.sub").First().Text()
	parts := strings.Split(sub, "|")
	last := parts[len(parts)-1]
	return strings.SplitN(last, " by ", 2)[0]
}

// freeleechFactor is the DownloadVolumeFactor: 0 when a `span.free` badge is present
// (freeleech), else 1 (full cost). UploadVolumeFactor is always 1.
func freeleechFactor(row *goquery.Selection) float64 {
	if row.Find("span.free").Length() > 0 {
		return 0
	}
	return 1
}

// absoluteURL resolves a site-relative href against the base URL, matching Prowlarr's
// `new Uri(BaseUrl + href.TrimStart('/'))`.
func (d *driver) absoluteURL(href string) string {
	return d.BaseURL + strings.TrimLeft(strings.TrimSpace(href), "/")
}

func attrOr(sel *goquery.Selection, name string) string {
	v, _ := sel.Attr(name)
	return v
}
