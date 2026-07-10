package hdbits

import (
	"encoding/json"
	"fmt"
	"net/url"
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
	// downloadPath / detailsPath are the Prowlarr-style URLs the parser rebuilds
	// (HDBitsParser GetDownloadUrl/GetInfoUrl): download.php carries the passkey, details.php
	// does not.
	downloadPath = "download.php"
	detailsPath  = "details.php"
	// statusSuccess is HDBits' StatusCode.Success; any non-zero status is an error envelope.
	statusSuccess = 0
	// statusAuthDataMissing / statusAuthFailed are the two credential statuses (4/5) that
	// map to a login failure; every other non-zero status is a generic parse error.
	statusAuthDataMissing = 4
	statusAuthFailed      = 5
	// xxxCategory is the type_category for XXX content (neutral up/down volume factors and
	// never a filename title); fullDiscMedium is type_medium 1 (Blu-ray/HD DVD full disc,
	// which also forces the name title).
	xxxCategory    = 7
	fullDiscMedium = 1
	// internalOrigin is type_origin 1 (an internal/half-leech release).
	internalOrigin = 1
	// customCatCutoff bounds the canonical newznab id range: the mapper synthesises a 1:1
	// custom category at ids >= 100000, which is discarded so each release carries exactly
	// one newznab category (matching Prowlarr).
	customCatCutoff = 100000
)

// halfLeechMediums is Prowlarr's _halfLeechMediums set (HdBitsMedium Bluray=1, Capture=4,
// Remux=5): a release on one of these mediums costs 50% download.
var halfLeechMediums = map[int64]struct{}{1: {}, 4: {}, 5: {}}

// hdbitsResponse is the {status,message,data} envelope. status==0 is success; a non-zero
// status carries an error message (which could echo the submitted credentials, so it is
// scrubbed before surfacing). Data is decoded as a flat array of release rows.
type hdbitsResponse struct {
	Status  int             `json:"status"`
	Message string          `json:"message"`
	Data    []hdbitsTorrent `json:"data"`
}

// hdbitsTorrent is one release row from data[]. Per Prowlarr's models id and hash are JSON
// strings and the remaining numerics are real JSON numbers, but flexInt is used on the
// numerics defensively (mirroring broadcastthenet/myanonamouse) so a string-encoded numeric
// never fails the page decode.
type hdbitsTorrent struct {
	ID             string    `json:"id"`
	Hash           string    `json:"hash"`
	Name           string    `json:"name"`
	Filename       string    `json:"filename"`
	Size           flexInt   `json:"size"`
	Seeders        flexInt   `json:"seeders"`
	Leechers       flexInt   `json:"leechers"`
	TimesCompleted flexInt   `json:"times_completed"`
	NumFiles       flexInt   `json:"numfiles"`
	Added          string    `json:"added"`
	Freeleech      string    `json:"freeleech"`
	TypeCategory   flexInt   `json:"type_category"`
	TypeMedium     flexInt   `json:"type_medium"`
	TypeOrigin     flexInt   `json:"type_origin"`
	Imdb           *imdbInfo `json:"imdb"`
	Tvdb           *tvdbInfo `json:"tvdb"`
}

// imdbInfo is the nested imdb object; only id and year are used.
type imdbInfo struct {
	ID   flexInt `json:"id"`
	Year flexInt `json:"year"`
}

// tvdbInfo is the nested tvdb object; only the series id is mapped (season/episode are
// request-side only).
type tvdbInfo struct {
	ID flexInt `json:"id"`
}

// flexInt unmarshals a JSON number OR a JSON string into an int64. HDBits wire-encodes most
// numerics as bare numbers, but a hostile/older server could string-encode one; this keeps a
// strict struct decode from rejecting the whole page (cf. broadcastthenet flexString).
type flexInt int64

func (n *flexInt) UnmarshalJSON(b []byte) error {
	if len(b) == 0 || string(b) == "null" {
		*n = 0
		return nil
	}
	if b[0] == '"' {
		var s string
		if err := json.Unmarshal(b, &s); err != nil {
			return fmt.Errorf("hdbits: decode numeric field: %w", err)
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
		return fmt.Errorf("hdbits: decode numeric field: %w", err)
	}
	*n = flexInt(v)
	return nil
}

func (n flexInt) int64() int64 { return int64(n) }

// parseReleases decodes an api/torrents JSON body into normalized releases. A status of 4/5
// (AuthDataMissing/AuthFailed) maps to login.ErrLoginFailed; any other non-zero status is a
// parse error. The message is scrubbed (it could echo username/passkey). Rows are mapped to
// releases (with the freeleech_only client-side filter) and sorted by ascending id for a
// deterministic feed.
func (d *driver) parseReleases(body []byte) ([]*normalizer.Release, error) {
	var resp hdbitsResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("hdbits: decode search response: %s: %w", apphttp.DecodeErrorDetail(err, body), search.ErrParseError)
	}
	if resp.Status != statusSuccess {
		return nil, d.statusError(resp.Status, resp.Message)
	}

	freeOnly := freeleechOnly(d.cfg)
	useFilenames := useFilenames(d.cfg)
	releases := make([]*normalizer.Release, 0, len(resp.Data))
	for i := range resp.Data {
		row := &resp.Data[i]
		if freeOnly && !isFreeleech(row.Freeleech) {
			continue
		}
		releases = append(releases, d.toRelease(row, useFilenames))
	}
	sortReleases(releases)
	native.TraceReleases(d.log, d.def.ID, releases)
	return releases, nil
}

// statusError maps a non-zero HDBits status to a sentinel error with the message scrubbed:
// AuthDataMissing (4) and AuthFailed (5) are credential failures (login.ErrLoginFailed); any
// other non-zero status is a parse error.
func (d *driver) statusError(status int, message string) error {
	msg := d.scrubSecrets(message)
	if status == statusAuthDataMissing || status == statusAuthFailed {
		return fmt.Errorf("hdbits: auth failed (status %d): %s: %w", status, msg, login.ErrLoginFailed)
	}
	return fmt.Errorf("hdbits: api error (status %d): %s: %w", status, msg, search.ErrParseError)
}

// toRelease maps one row to a normalized release. Title follows Prowlarr (filename sans
// .torrent unless XXX, full disc, or use_filenames is off); the category is the single
// canonical newznab id for type_category; the link is the rebuilt download.php URL (routed
// through /dl by NeedsResolver, so its passkey never reaches the feed); the volume factors
// follow the freeleech/XXX/half-leech rules.
func (d *driver) toRelease(row *hdbitsTorrent, useFilenames bool) *normalizer.Release {
	seeders := row.Seeders.int64()
	leechers := row.Leechers.int64()
	rel := &normalizer.Release{
		Title:                title(row, useFilenames),
		InfoHash:             row.Hash,
		Link:                 d.downloadURL(row.ID),
		Details:              d.detailsURL(row.ID),
		Categories:           d.categories(row.TypeCategory.int64()),
		Size:                 row.Size.int64(),
		Files:                row.NumFiles.int64(),
		Grabs:                row.TimesCompleted.int64(),
		Seeders:              seeders,
		Leechers:             leechers,
		Peers:                seeders + leechers,
		PublishDate:          publishDate(row.Added),
		DownloadVolumeFactor: downloadVolumeFactor(row),
		UploadVolumeFactor:   uploadVolumeFactor(row),
	}
	if row.Imdb != nil {
		rel.IMDBID = fullIMDBID(row.Imdb.ID.int64())
		rel.Year = row.Imdb.Year.int64()
	}
	if row.Tvdb != nil {
		rel.TVDBID = row.Tvdb.ID.int64()
	}
	return rel
}

// title composes the release title: the filename with a trailing ".torrent" stripped when
// use_filenames is on and the row is neither XXX (cat 7) nor a full disc (medium 1) and the
// filename is non-empty; otherwise the name (Prowlarr HDBitsParser).
func title(row *hdbitsTorrent, useFilenames bool) string {
	cat := row.TypeCategory.int64()
	medium := row.TypeMedium.int64()
	fn := strings.TrimSpace(row.Filename)
	if cat != xxxCategory && medium != fullDiscMedium && useFilenames && fn != "" {
		return stripTorrentExt(fn)
	}
	return strings.TrimSpace(row.Name)
}

// stripTorrentExt removes a trailing ".torrent" (case-insensitive), matching Prowlarr's
// Replace(".torrent", "", InvariantCultureIgnoreCase). Prowlarr replaces every occurrence,
// but the suffix is the only realistic position, so a suffix trim is the faithful behavior.
func stripTorrentExt(name string) string {
	if len(name) >= len(".torrent") && strings.EqualFold(name[len(name)-len(".torrent"):], ".torrent") {
		return name[:len(name)-len(".torrent")]
	}
	return name
}

// categories returns the single canonical newznab category for a type_category int. The
// mapper also synthesises a 1:1 custom id (>= customCatCutoff) which is discarded so the
// release carries exactly one category (matching Prowlarr, which emits one).
func (d *driver) categories(typeCategory int64) []int {
	for _, c := range d.caps.CategoryMap.MapTrackerCatToNewznab(strconv.FormatInt(typeCategory, 10)) {
		if c < customCatCutoff {
			return []int{c}
		}
	}
	return nil
}

// downloadVolumeFactor reproduces Prowlarr's GetDownloadVolumeFactor: freeleech is free (0),
// XXX is free (0), a half-leech medium / internal origin / TV / Documentary is 50% (0.5),
// everything else full price (1).
func downloadVolumeFactor(row *hdbitsTorrent) float64 {
	if isFreeleech(row.Freeleech) {
		return 0
	}
	cat := row.TypeCategory.int64()
	if cat == xxxCategory {
		return 0
	}
	if _, half := halfLeechMediums[row.TypeMedium.int64()]; half {
		return 0.5
	}
	if row.TypeOrigin.int64() == internalOrigin || cat == 2 || cat == 3 {
		return 0.5
	}
	return 1
}

// uploadVolumeFactor reproduces Prowlarr's GetUploadVolumeFactor: XXX uploads count zero, all
// others 1x.
func uploadVolumeFactor(row *hdbitsTorrent) float64 {
	if row.TypeCategory.int64() == xxxCategory {
		return 0
	}
	return 1
}

// isFreeleech reports whether the freeleech string is "yes" (Prowlarr compares it exactly);
// any other value (incl. "no") is non-freeleech.
func isFreeleech(s string) bool {
	return strings.EqualFold(strings.TrimSpace(s), "yes")
}

// addedLayouts are the `added` wire formats tried in order. HDBits' captured feed sends an
// offset WITHOUT a colon (e.g. "2015-04-04T20:30:46+0000"), which time.RFC3339 rejects, so
// the no-colon offset form ("...-0700") must be tried too. The bare and space forms are
// assumed UTC (mirrors avistaz/parsePublishDate); Prowlarr deserializes via the lenient
// Newtonsoft DateTime, so harbrr must accept the same variants.
var addedLayouts = []string{
	time.RFC3339Nano,
	time.RFC3339,
	"2006-01-02T15:04:05-0700",
	"2006-01-02T15:04:05",
	"2006-01-02 15:04:05",
}

// publishDate parses the `added` field to UTC RFC3339 (Prowlarr's
// result.Added.ToUniversalTime()). It tries each known layout in turn (HDBits actually sends
// a no-colon offset, which RFC3339 alone rejects). An unparseable/empty value yields ""
// rather than failing the whole page (a single bad date must not drop the result set).
func publishDate(added string) string {
	added = strings.TrimSpace(added)
	if added == "" {
		return ""
	}
	for _, layout := range addedLayouts {
		if t, err := time.Parse(layout, added); err == nil {
			return t.UTC().Format(time.RFC3339)
		}
	}
	return ""
}

// downloadURL rebuilds the Prowlarr download URL: {base}download.php?id={id}&passkey=
// {passkey}. The passkey is a secret; this URL is served only through /dl (the proxy keeps
// it out of the feed) and is never logged.
func (d *driver) downloadURL(id string) string {
	params := url.Values{}
	params.Set("id", id)
	params.Set("passkey", strings.TrimSpace(d.cfg["passkey"]))
	return d.baseURL + downloadPath + "?" + params.Encode()
}

// detailsURL rebuilds the Prowlarr info URL: {base}details.php?id={id} (no secret).
func (d *driver) detailsURL(id string) string {
	params := url.Values{}
	params.Set("id", id)
	return d.baseURL + detailsPath + "?" + params.Encode()
}

// fullIMDBID renders a bare imdb id as the canonical "tt"+7-digit form harbrr stores
// (Prowlarr keeps the bare int; the normalizer's IMDBID is the tt-prefixed string). A
// non-positive id (absent imdb) yields "".
func fullIMDBID(id int64) string {
	if id <= 0 {
		return ""
	}
	return fmt.Sprintf("tt%07d", id)
}

// freeleechOnly reports whether the freeleech_only checkbox is enabled (Prowlarr's
// FreeleechOnly, default false). harbrr stores a checked checkbox as Jackett's "True"
// sentinel; common truthy spellings are also accepted.
func freeleechOnly(cfg map[string]string) bool {
	switch strings.ToLower(strings.TrimSpace(cfg["freeleech_only"])) {
	case "true", "1", "on", "yes":
		return true
	default:
		return false
	}
}

// useFilenames reports whether filename-derived titles are used (Prowlarr's UseFilenames,
// default TRUE). Only an explicit falsy value turns it off; an absent/blank setting keeps
// the default-on behavior.
func useFilenames(cfg map[string]string) bool {
	switch strings.ToLower(strings.TrimSpace(cfg["use_filenames"])) {
	case "false", "0", "off", "no":
		return false
	default:
		return true
	}
}

// scrubSecrets removes the configured username and passkey from s so a server echo (e.g. in
// an error message) cannot leak either credential (mirrors broadcastthenet.scrubAPIKey /
// filelist.scrubPasskey). Both ride in the secret-bearing POST body.
func (d *driver) scrubSecrets(s string) string {
	for _, k := range []string{"passkey", "username"} {
		if v := strings.TrimSpace(d.cfg[k]); v != "" {
			s = strings.ReplaceAll(s, v, "[redacted]")
		}
	}
	return s
}

// sortReleases orders releases by ascending numeric id (the data[] order is server-defined;
// a stable id order keeps the feed and tests deterministic). Ties break on the raw download
// URL (unique per id) so the order is total.
func sortReleases(releases []*normalizer.Release) {
	sort.SliceStable(releases, func(i, j int) bool {
		ki, kj := idFromLink(releases[i].Link), idFromLink(releases[j].Link)
		if ki != kj {
			return ki < kj
		}
		return releases[i].Link < releases[j].Link
	})
}

// idFromLink extracts the numeric id query param from a rebuilt download URL for the sort
// key; an unparseable id sorts as 0.
func idFromLink(link string) int64 {
	u, err := url.Parse(link)
	if err != nil {
		return 0
	}
	id, err := strconv.ParseInt(u.Query().Get("id"), 10, 64)
	if err != nil {
		return 0
	}
	return id
}
