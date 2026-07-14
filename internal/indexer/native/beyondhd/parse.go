package beyondhd

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	apphttp "github.com/autobrr/harbrr/internal/http"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/login"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/normalizer"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
	"github.com/autobrr/harbrr/internal/indexer/native"
)

const (
	// statusFailure is the only failure sentinel: BeyondHD's status_code==0 means the
	// request failed (the parser throws with status_message); any non-zero value is a
	// success, so the parser treats != 0 as OK rather than hard-requiring ==1 (Prowlarr
	// BeyondHDParser gates on `== 0`).
	statusFailure = 0
	// customCatCutoff bounds the canonical newznab id range: the caps mapper synthesises a
	// 1:1 custom category at ids >= 100000, which is discarded so each release carries
	// exactly one newznab category (matching Prowlarr, which emits one).
	customCatCutoff = 100000
	// minimumSeedTime is BeyondHD's seed-time requirement in seconds (the Prowlarr literal,
	// 172800 = 48h — the code comment there says "120 hours" but the literal wins).
	minimumSeedTime = 172800
	// invalidKeyMarker is the substring BeyondHD returns when the api_key is rejected; it
	// maps to a login failure regardless of the status_code. It is a server message marker,
	// not a credential value.
	invalidKeyMarker = "Invalid API Key" //nolint:gosec // G101: response-message marker, not a credential
)

// promo download-volume factors, in descending discount order (Prowlarr
// GetDownloadVolumeFactor): a 75% promo costs 25% download, 50% costs 50%, 25% costs 75%.
const (
	promo75Factor = 0.25
	promo50Factor = 0.5
	promo25Factor = 0.75
)

// bhdResponse is the {status_code,status_message,results[]} envelope. status_code==0 is a
// failure carrying a status_message (which could echo a credential, so it is scrubbed);
// results is a FLAT array of release rows (null/absent on a zero-result or error page).
type bhdResponse struct {
	StatusCode    int          `json:"status_code"`
	StatusMessage string       `json:"status_message"`
	Results       []bhdTorrent `json:"results"`
}

// bhdTorrent is one release row. Per Prowlarr's models the numerics are real JSON numbers
// and the ids are strings, but flexInt/flexBool are used defensively (mirroring
// hdbits/broadcastthenet) so a string-encoded value never fails the page decode.
type bhdTorrent struct {
	Name           string   `json:"name"`
	InfoHash       string   `json:"info_hash"`
	Category       string   `json:"category"`
	Type           string   `json:"type"`
	Size           flexInt  `json:"size"`
	TimesCompleted flexInt  `json:"times_completed"`
	Seeders        flexInt  `json:"seeders"`
	Leechers       flexInt  `json:"leechers"`
	CreatedAt      string   `json:"created_at"`
	URL            string   `json:"url"`
	DownloadURL    string   `json:"download_url"`
	ImdbID         string   `json:"imdb_id"`
	TmdbID         string   `json:"tmdb_id"`
	Freeleech      flexBool `json:"freeleech"`
	Promo25        flexBool `json:"promo25"`
	Promo50        flexBool `json:"promo50"`
	Promo75        flexBool `json:"promo75"`
	Limited        flexBool `json:"limited"`
	Exclusive      flexBool `json:"exclusive"`
	Internal       flexBool `json:"internal"`
}

// flexInt unmarshals a JSON number OR a JSON string into an int64. BeyondHD wire-encodes
// the numerics as bare numbers, but a hostile/older server could string-encode one; this
// keeps a strict struct decode from rejecting the whole page (cf. hdbits flexInt).
type flexInt int64

func (n *flexInt) UnmarshalJSON(b []byte) error {
	if len(b) == 0 || string(b) == "null" {
		*n = 0
		return nil
	}
	if b[0] == '"' {
		var s string
		if err := json.Unmarshal(b, &s); err != nil {
			return fmt.Errorf("beyondhd: decode numeric field: %w", err)
		}
		v, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
		if err != nil {
			*n = 0
			return nil
		}
		*n = flexInt(v)
		return nil
	}
	var v int64
	if err := json.Unmarshal(b, &v); err != nil {
		return fmt.Errorf("beyondhd: decode numeric field: %w", err)
	}
	*n = flexInt(v)
	return nil
}

// flexBool unmarshals a JSON boolean, a number (1/0), or a string ("true"/"false"/"1"/"0")
// into a bool. BeyondHD wire-encodes these as real JSON booleans, but a hostile/older server
// could send 1/0 or a quoted form; anything unrecognized degrades to false rather than
// failing the whole-page decode (mirroring flexInt's degrade-to-0, cf. hdbits).
type flexBool bool

func (b *flexBool) UnmarshalJSON(data []byte) error {
	s := strings.TrimSpace(string(data))
	// Unwrap a quoted form ("true"/"1"/…) to its inner token.
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		s = s[1 : len(s)-1]
	}
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "true", "1":
		*b = true
	default:
		*b = false
	}
	return nil
}

func (n flexInt) int64() int64 { return int64(n) }

// parseReleases decodes an api/torrents JSON body into normalized releases. A body that
// is non-JSON or whose message contains "Invalid API Key" maps to login.ErrLoginFailed; a
// status_code==0 with any other message is a generic parse error. The message is scrubbed
// (it could echo the api_key/rsskey). Rows are mapped to releases and sorted by descending
// publish date (Prowlarr orders by PublishDate desc) for a deterministic feed.
func (d *driver) parseReleases(body []byte) ([]*normalizer.Release, error) {
	if containsInvalidKey(body) {
		return nil, fmt.Errorf("beyondhd: %w", login.ErrLoginFailed)
	}
	var resp bhdResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("beyondhd: decode search response: %s: %w", apphttp.DecodeErrorDetail(err, body), search.ErrParseError)
	}
	if resp.StatusCode == statusFailure {
		return nil, d.statusError(resp.StatusMessage)
	}

	releases := make([]*normalizer.Release, 0, len(resp.Results))
	for i := range resp.Results {
		releases = append(releases, d.toRelease(&resp.Results[i]))
	}
	sortReleases(releases)
	native.TraceReleases(d.Log, d.Def.ID, releases)
	return releases, nil
}

// containsInvalidKey reports whether the raw body carries the "Invalid API Key" marker
// BeyondHD returns for a rejected key (it can arrive inside the JSON status_message or as a
// bare text/html body), which maps to a login failure rather than a generic parse error.
func containsInvalidKey(body []byte) bool {
	return strings.Contains(string(body), invalidKeyMarker)
}

// statusError maps a status_code==0 failure to a sentinel with the message scrubbed: a
// message naming an invalid key is a login failure; any other failure is a generic indexer
// parse error (search.ErrParseError).
func (d *driver) statusError(message string) error {
	msg := d.scrubSecrets(message)
	if strings.Contains(message, invalidKeyMarker) {
		return fmt.Errorf("beyondhd: %s: %w", msg, login.ErrLoginFailed)
	}
	return fmt.Errorf("beyondhd: api error: %s: %w", msg, search.ErrParseError)
}

// toRelease maps one row to a normalized release. Title=name verbatim (Prowlarr does not
// compose it from type/resolution); the category is the single canonical newznab id for
// the row's `category` description string; Link=download_url (routed through /dl by
// NeedsResolver, so its embedded rsskey never reaches the feed); Peers=Seeders+Leechers;
// Grabs=times_completed; the publish date is created_at as UTC RFC3339; the volume factors
// follow the freeleech/limited/promo matrix.
func (d *driver) toRelease(row *bhdTorrent) *normalizer.Release {
	seeders := row.Seeders.int64()
	leechers := row.Leechers.int64()
	rel := &normalizer.Release{
		Title:                row.Name,
		InfoHash:             row.InfoHash,
		Link:                 row.DownloadURL,
		Details:              row.URL,
		Categories:           d.categories(row.Category),
		Size:                 row.Size.int64(),
		Grabs:                row.TimesCompleted.int64(),
		Seeders:              seeders,
		Leechers:             leechers,
		Peers:                seeders + leechers,
		PublishDate:          publishDate(row.CreatedAt),
		DownloadVolumeFactor: downloadVolumeFactor(row),
		UploadVolumeFactor:   1,
		MinimumRatio:         1,
		MinimumSeedTime:      minimumSeedTime,
		IMDBID:               canonicalIMDB(row.ImdbID),
		TMDBID:               tmdbID(row.TmdbID),
	}
	return rel
}

// categories returns the single canonical newznab category for a BeyondHD `category`
// description string ("Movies"/"TV"), mapped through the site caps. The mapper also
// synthesises a 1:1 custom id (>= customCatCutoff) which is discarded so the release
// carries exactly one category (matching Prowlarr).
func (d *driver) categories(category string) []int {
	for _, c := range d.Caps.CategoryMap.MapTrackerCatDescToNewznab(category) {
		if c < customCatCutoff {
			return []int{c}
		}
	}
	return nil
}

// downloadVolumeFactor reproduces Prowlarr's GetDownloadVolumeFactor: a freeleech or
// limited release is free (0); otherwise the largest active promo discount applies
// (75%->0.25, 50%->0.5, 25%->0.75); everything else is full price (1).
func downloadVolumeFactor(row *bhdTorrent) float64 {
	switch {
	case bool(row.Freeleech) || bool(row.Limited):
		return 0
	case bool(row.Promo75):
		return promo75Factor
	case bool(row.Promo50):
		return promo50Factor
	case bool(row.Promo25):
		return promo25Factor
	default:
		return 1
	}
}

// publishDate parses created_at to UTC RFC3339 (Prowlarr parses it AssumeUniversal). The
// observed wire form is "2006-01-02 15:04:05"; the RFC3339 variants are accepted too. An
// unparseable/empty value yields "" rather than failing the whole page.
func publishDate(created string) string {
	created = strings.TrimSpace(created)
	if created == "" {
		return ""
	}
	for _, layout := range createdLayouts {
		if t, err := time.Parse(layout, created); err == nil {
			return t.UTC().Format(time.RFC3339)
		}
	}
	return ""
}

// createdLayouts are the created_at wire formats tried in order: the observed space form
// (assumed UTC, matching Prowlarr's AssumeUniversal) plus the RFC3339 variants.
var createdLayouts = []string{
	"2006-01-02 15:04:05",
	time.RFC3339Nano,
	time.RFC3339,
	"2006-01-02T15:04:05",
}

// canonicalIMDB normalizes BeyondHD's imdb_id to the canonical "tt"+7-digit form harbrr
// stores (mirroring search.imdbID and hdbits.fullIMDBID; Prowlarr runs it through
// ParseUtil.GetImdbId). The wire value is unreliable — a bare numeric ("0133093"), an
// under-padded id, or even a junk URL can arrive — so a leading "tt" is stripped and the
// remainder must parse as a positive int; anything else (non-numeric, zero, a URL) yields
// "" rather than reaching the feed verbatim.
func canonicalIMDB(raw string) string {
	s := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(raw)), "tt")
	if s == "" {
		return ""
	}
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 {
		return ""
	}
	return fmt.Sprintf("tt%07d", n)
}

// tmdbID parses BeyondHD's tmdb_id (the string form "movie/<id>") into the bare numeric id
// (Prowlarr takes the segment after the '/'). A blank or non-"prefix/number" value yields
// 0 (absent).
func tmdbID(raw string) int64 {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0
	}
	parts := strings.Split(raw, "/")
	id, err := strconv.ParseInt(strings.TrimSpace(parts[len(parts)-1]), 10, 64)
	if err != nil {
		return 0
	}
	return id
}

// scrubSecrets removes the configured api_key and rsskey from s so a server echo (e.g. in
// an error message) cannot leak either (mirrors hdbits.scrubSecrets). The api_key rides in
// the secret-bearing URL path; the rsskey rides in the body + the download URL.
func (d *driver) scrubSecrets(s string) string {
	for _, k := range []string{"api_key", "rsskey"} {
		if v := strings.TrimSpace(d.Cfg[k]); v != "" {
			s = strings.ReplaceAll(s, v, "[redacted]")
		}
	}
	return s
}

// sortReleases orders releases by descending publish date (Prowlarr orders by PublishDate
// desc); ties break on the download URL (unique per release) so the order is total and
// deterministic even when two rows share a timestamp.
func sortReleases(releases []*normalizer.Release) {
	sort.SliceStable(releases, func(i, j int) bool {
		if releases[i].PublishDate != releases[j].PublishDate {
			return releases[i].PublishDate > releases[j].PublishDate
		}
		return releases[i].Link < releases[j].Link
	})
}
