package myanonamouse

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	apphttp "github.com/autobrr/harbrr/internal/http"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/normalizer"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
)

// minimumSeedTime is MAM's fixed 72h seed requirement (Prowlarr).
const minimumSeedTime = 259200

// mamResponse / mamRelease mirror the loadSearchJSONbasic.php JSON (the subset harbrr
// consumes). Error carries the server's error string; Data is the result rows. Data
// is a pointer so a 2xx body with no `data` array (a `{}`, `null`, or maintenance
// stub) is distinguishable from a legitimate empty result and is a parse error.
type mamResponse struct {
	Error string        `json:"error"`
	Data  *[]mamRelease `json:"data"`
}

// mamRelease is one result row. Numeric stats are pointers so an absent field is 0,
// not a decode error. AuthorInfo is a STRINGIFIED (possibly malformed) JSON dict of
// id→name, parsed defensively in authorNames. Size is a human-readable string (e.g.
// "1.29 GB"). Category is the tracker category id (a string).
type mamRelease struct {
	ID                int64         `json:"id"`
	Title             string        `json:"title"`
	AuthorInfo        string        `json:"author_info"`
	Category          mamFlexString `json:"category"`
	MainCat           mamFlexString `json:"main_cat"`
	Added             string        `json:"added"`
	Size              string        `json:"size"`
	Seeders           *int64        `json:"seeders"`
	Leechers          *int64        `json:"leechers"`
	TimesCompleted    *int64        `json:"times_completed"`
	NumFiles          *int64        `json:"numfiles"`
	Free              mamFlexBool   `json:"free"`
	PersonalFreeleech mamFlexBool   `json:"personal_freeleech"`
	FlVIP             mamFlexBool   `json:"fl_vip"`
	DL                string        `json:"dl"`
}

// mamFlexString unmarshals a JSON string OR number into a string. MAM's category
// ids (category, main_cat) arrive as JSON numbers from the live API but as strings
// in the documented contract / earlier goldens — accept both so a strict struct
// decode doesn't reject the live body (cf. the FileList int-flags live fix #46).
type mamFlexString string

func (s *mamFlexString) UnmarshalJSON(b []byte) error {
	if len(b) == 0 || string(b) == "null" {
		*s = ""
		return nil
	}
	if b[0] == '"' {
		var str string
		if err := json.Unmarshal(b, &str); err != nil {
			return fmt.Errorf("myanonamouse: decode string id: %w", err)
		}
		*s = mamFlexString(str)
		return nil
	}
	*s = mamFlexString(b) // a JSON number: keep its literal text as the id
	return nil
}

// mamFlexBool unmarshals a JSON bool OR number (0/1) into a bool. MAM's freeleech
// flags (free, personal_freeleech, fl_vip) arrive as integers from the live API but
// as booleans in the documented contract — accept both.
type mamFlexBool bool

func (f *mamFlexBool) UnmarshalJSON(b []byte) error {
	switch string(b) {
	case "true", "1", `"1"`, `"true"`:
		*f = true
	default:
		*f = false
	}
	return nil
}

// errNothingReturned is the MAM "no results" Error sentinel prefix; Prowlarr ignores
// an Error matching "Nothing returned, out of …" and treats it as zero results.
const errNothingReturned = "Nothing returned, out of"

// parseReleases decodes a 2xx loadSearchJSONbasic.php body into normalized releases,
// reproducing the MyAnonamouse parser: title (+ appended author), the human-readable
// size, the freeleech-derived download factor, the category, and the download URL —
// sorted by publish date descending. A non-"Nothing returned" Error, a missing data
// array, a malformed size, or an unparseable date is a parse error (Prowlarr throws).
func (d *driver) parseReleases(body []byte) ([]*normalizer.Release, error) {
	var resp mamResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("myanonamouse: decode search response: %s: %w", apphttp.DecodeErrorDetail(err, body), search.ErrParseError)
	}
	if strings.HasPrefix(resp.Error, errNothingReturned) {
		return nil, nil
	}
	if resp.Error != "" {
		return nil, fmt.Errorf("myanonamouse: search error %q: %w", d.scrubSecret(resp.Error), search.ErrParseError)
	}
	if resp.Data == nil {
		return nil, fmt.Errorf("myanonamouse: search response carried no data array: %w", search.ErrParseError)
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

// toRelease maps one DTO row to a normalized release. Link is the explicitly-built
// download URL (the served feed routes it through /dl because NeedsResolver=true);
// the author (from author_info) is appended to the title and set as Author. A
// freeleech row (free / personal_freeleech / fl_vip) carries DownloadVolumeFactor 0.
func (d *driver) toRelease(row *mamRelease) (*normalizer.Release, error) {
	size, err := parseSize(row.Size)
	if err != nil {
		return nil, err
	}
	published, err := parsePublishDate(row.Added)
	if err != nil {
		return nil, err
	}
	authors := authorNames(row.AuthorInfo)
	rel := &normalizer.Release{
		Title:                titleWithAuthors(row.Title, authors),
		Author:               strings.Join(authors, ", "),
		Link:                 d.downloadURL(row),
		Details:              d.detailsURL(row),
		Categories:           d.categories(row),
		Size:                 size,
		Files:                deref(row.NumFiles),
		Grabs:                deref(row.TimesCompleted),
		Seeders:              deref(row.Seeders),
		Leechers:             deref(row.Leechers),
		Peers:                deref(row.Seeders) + deref(row.Leechers),
		PublishDate:          published.Format(time.RFC3339),
		DownloadVolumeFactor: downloadVolumeFactor(row),
		UploadVolumeFactor:   1,
		MinimumRatio:         1,
		MinimumSeedTime:      minimumSeedTime,
	}
	return rel, nil
}

// downloadURL builds the .torrent URL explicitly (Prowlarr's approach) from the dl
// hash and the row id: {base}tor/download.php/{dl}?tid={id}. Building it explicitly
// (rather than trusting an API link) keeps it deterministic and immune to a redacted
// field.
func (d *driver) downloadURL(row *mamRelease) string {
	return d.baseURL + "tor/download.php/" + row.DL + "?tid=" + strconv.FormatInt(row.ID, 10)
}

// detailsURL is the torrent info page: {base}t/{id}.
func (d *driver) detailsURL(row *mamRelease) string {
	return d.baseURL + "t/" + strconv.FormatInt(row.ID, 10)
}

// categories maps the row's category id through the site caps. main_cat is the
// fallback when the row has no specific category. The result is de-duplicated and
// sorted for a deterministic feed.
func (d *driver) categories(row *mamRelease) []int {
	id := strings.TrimSpace(string(row.Category))
	if id == "" {
		id = strings.TrimSpace(string(row.MainCat))
	}
	mapped := d.caps.CategoryMap.MapTrackerCatToNewznab(id)
	seen := make(map[int]struct{}, len(mapped))
	out := make([]int, 0, len(mapped))
	for _, c := range mapped {
		if _, dup := seen[c]; dup {
			continue
		}
		seen[c] = struct{}{}
		out = append(out, c)
	}
	sort.Ints(out)
	return out
}

// downloadVolumeFactor is 0 for a freeleech row (free / personal_freeleech / fl_vip),
// else 1 (full cost), matching Prowlarr's isFreeLeech.
func downloadVolumeFactor(row *mamRelease) float64 {
	if bool(row.Free) || bool(row.PersonalFreeleech) || bool(row.FlVIP) {
		return 0
	}
	return 1
}

// titleWithAuthors appends the parsed author names to the title in "Title by A, B"
// form (matching Prowlarr), or returns the bare title when there is no author.
func titleWithAuthors(title string, authors []string) string {
	title = strings.TrimSpace(title)
	if len(authors) == 0 {
		return title
	}
	return title + " by " + strings.Join(authors, ", ")
}

// authorNames parses the stringified author_info dict (id→name) defensively: it is a
// JSON string whose value is itself a (sometimes malformed) JSON object, so a decode
// failure yields no authors rather than an error or a panic. Names are returned in a
// deterministic order (the dict has no inherent order in Go).
func authorNames(authorInfo string) []string {
	authorInfo = strings.TrimSpace(authorInfo)
	if authorInfo == "" {
		return nil
	}
	var dict map[string]string
	if json.Unmarshal([]byte(authorInfo), &dict) != nil {
		return nil
	}
	names := make([]string, 0, len(dict))
	for _, name := range dict {
		if name = strings.TrimSpace(name); name != "" {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

// parsePublishDate parses the `added` "yyyy-MM-dd HH:mm:ss" timestamp as UTC
// (Prowlarr's ParseExact with AssumeUniversal). An unparseable value is a parse error.
func parsePublishDate(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	t, err := time.Parse("2006-01-02 15:04:05", s)
	if err != nil {
		return time.Time{}, fmt.Errorf("myanonamouse: unparseable added date %q: %w", s, search.ErrParseError)
	}
	return t.UTC(), nil
}

// sizeUnits maps a human-readable size suffix to its byte multiplier. MAM uses binary
// (1024-based) units.
var sizeUnits = map[string]float64{
	"B":   1,
	"KB":  1 << 10,
	"KIB": 1 << 10,
	"MB":  1 << 20,
	"MIB": 1 << 20,
	"GB":  1 << 30,
	"GIB": 1 << 30,
	"TB":  1 << 40,
	"TIB": 1 << 40,
	"PB":  1 << 50,
	"PIB": 1 << 50,
}

// parseSize converts a human-readable size string (e.g. "1.29 GB") to bytes. The
// number and unit may be space-separated or not; the unit is case-insensitive. MAM
// formats the amount en-US style, so sizes ≥ 1000 carry a comma thousands separator
// (e.g. "1,001.6 MiB") which is stripped before parsing. A missing or unrecognized
// unit, or a non-numeric amount, is a parse error.
func parseSize(s string) (int64, error) {
	fields := strings.Fields(s)
	if len(fields) < 2 {
		return 0, fmt.Errorf("myanonamouse: unparseable size %q: %w", s, search.ErrParseError)
	}
	amount, err := strconv.ParseFloat(strings.ReplaceAll(fields[0], ",", ""), 64)
	if err != nil {
		return 0, fmt.Errorf("myanonamouse: unparseable size amount %q: %w", s, search.ErrParseError)
	}
	mult, ok := sizeUnits[strings.ToUpper(fields[1])]
	if !ok {
		return 0, fmt.Errorf("myanonamouse: unknown size unit %q: %w", fields[1], search.ErrParseError)
	}
	return int64(math.Round(amount * mult)), nil
}

func deref(p *int64) int64 {
	if p == nil {
		return 0
	}
	return *p
}
