package broadcastthenet

import (
	"bytes"
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

// tvCategory is the newznab TV root (5000); the parser falls back to it when a
// torrent's Resolution maps to no specific TV/SD|HD|UHD category (Prowlarr
// SetCapabilities). It also bounds the "canonical" newznab id range used to discard
// the mapper's synthesised 1:1 custom category (those ids are ≥ 100000).
const (
	tvCategory      = 5000
	customCatCutoff = 100000
)

// invalidAPIKeyCode is the JSON-RPC error code BTN returns for a rejected API key
// ({"error":{"code":-32001,"message":"Invalid API Key"}}); it maps to a login failure.
const invalidAPIKeyCode = -32001

// btnResponse is the JSON-RPC 2.0 envelope getTorrents returns. Result is a pointer so
// a {"result":null,...} error body is distinguishable from an empty success result.
// Error is the JSON-RPC error object (present only on failure).
type btnResponse struct {
	Result *btnResult `json:"result"`
	Error  *btnError  `json:"error"`
}

// btnResult is the success payload. Results is the total match count (BTN sends it as a
// JSON string, so native.FlexString decodes it). Torrents is kept as RawMessage because PHP
// serializes an empty associative array as a JSON ARRAY (`[]`) rather than an object
// (`{}`): decoding it straight into a map would fail the struct decode on a zero-result
// page, so the array wire form is tolerated and only an object (`{…}`) is unmarshalled
// into the id→torrent map (cf. Prowlarr, which short-circuits on a zero count).
type btnResult struct {
	Results  native.FlexString `json:"results"`
	Torrents json.RawMessage   `json:"torrents"`
}

// btnError is the JSON-RPC error object.
type btnError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// btnTorrent is one torrent row. BTN wire-encodes EVERY field as a JSON string,
// including the numerics, so every numeric uses native.FlexString and is parsed at mapping
// time (mirroring myanonamouse's tolerant decode and Prowlarr's JSON converter).
type btnTorrent struct {
	TorrentID   native.FlexString `json:"TorrentID"`
	GroupID     native.FlexString `json:"GroupID"`
	ReleaseName string            `json:"ReleaseName"`
	Category    string            `json:"Category"`
	Resolution  string            `json:"Resolution"`
	Origin      string            `json:"Origin"`
	Size        native.FlexString `json:"Size"`
	Time        native.FlexString `json:"Time"`
	Snatched    native.FlexString `json:"Snatched"`
	Seeders     native.FlexString `json:"Seeders"`
	Leechers    native.FlexString `json:"Leechers"`
	TvdbID      native.FlexString `json:"TvdbID"`
	TvrageID    native.FlexString `json:"TvrageID"`
	ImdbID      native.FlexString `json:"ImdbID"`
	InfoHash    string            `json:"InfoHash"`
	DownloadURL string            `json:"DownloadURL"`
}

// parseReleases decodes a getTorrents JSON-RPC body into normalized releases. It checks
// the error envelope first: a -32001 ("Invalid API Key") maps to login.ErrLoginFailed,
// any other JSON-RPC error (or a null result) is a parse error with the apikey scrubbed.
// The torrents map iterates in an unspecified order, so releases are sorted by numeric
// TorrentID for a deterministic feed (and stable tests).
func (d *driver) parseReleases(body []byte) ([]*normalizer.Release, error) {
	var resp btnResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("broadcastthenet: decode search response: %s: %w", apphttp.DecodeErrorDetail(err, body), search.ErrParseError)
	}
	if resp.Error != nil {
		if resp.Error.Code == invalidAPIKeyCode {
			return nil, fmt.Errorf("broadcastthenet: %s: %w", d.Scrub(resp.Error.Message), login.ErrLoginFailed)
		}
		return nil, fmt.Errorf("broadcastthenet: api error %d: %s: %w",
			resp.Error.Code, d.Scrub(resp.Error.Message), search.ErrParseError)
	}
	if resp.Result == nil {
		return nil, fmt.Errorf("broadcastthenet: null result: %w", search.ErrParseError)
	}

	torrents, err := decodeTorrents(resp.Result)
	if err != nil {
		return nil, err
	}
	releases := make([]*sortableRelease, 0, len(torrents))
	for id := range torrents {
		t := torrents[id]
		releases = append(releases, d.toRelease(id, &t))
	}
	sortReleases(releases)
	out := releasesOnly(releases)
	native.TraceReleases(d.Log, d.Def.ID, out)
	return out, nil
}

// decodeTorrents resolves the result's torrents field into the id→torrent map. A
// zero-result page is short-circuited: PHP serializes an empty associative array as `[]`,
// so for results==0 any non-object shape (`[]`, null, absent) is accepted as zero
// torrents rather than a decode failure. For a POSITIVE result count, though, the field
// must be a JSON object — a non-object there is a malformed response, not an empty page,
// so it is reported as a parse error instead of silently dropping the results.
func decodeTorrents(result *btnResult) (map[string]btnTorrent, error) {
	if result.Results.Int64() == 0 {
		return map[string]btnTorrent{}, nil
	}
	raw := bytes.TrimSpace(result.Torrents)
	if len(raw) == 0 || raw[0] != '{' {
		return nil, fmt.Errorf("broadcastthenet: torrents not an object for %d results: %w",
			result.Results.Int64(), search.ErrParseError)
	}
	var torrents map[string]btnTorrent
	if err := json.Unmarshal(raw, &torrents); err != nil {
		return nil, fmt.Errorf("broadcastthenet: decode torrents: %s: %w", apphttp.DecodeErrorDetail(err, raw), search.ErrParseError)
	}
	return torrents, nil
}

// sortReleases orders releases by numeric TorrentID ascending, breaking ties on the raw
// map-key string (always unique, so the order is TOTAL and deterministic even when the
// numeric key is 0 for an unparseable id — the map otherwise iterates in random order).
func sortReleases(releases []*sortableRelease) {
	sort.Slice(releases, func(i, j int) bool {
		if releases[i].torrentIDSortKey != releases[j].torrentIDSortKey {
			return releases[i].torrentIDSortKey < releases[j].torrentIDSortKey
		}
		return releases[i].mapKey < releases[j].mapKey
	})
}

// sortableRelease pairs a release with its numeric TorrentID and the raw map-key string
// so the deterministic sort does not re-parse the id during comparison and has a unique
// tie-breaker (the map key) when two ids parse to the same int64 (e.g. both unparseable
// → 0).
type sortableRelease struct {
	*normalizer.Release
	torrentIDSortKey int64
	mapKey           string
}

// toRelease maps one torrent row to a normalized release. Title=ReleaseName,
// Link=DownloadURL (served only through /dl because NeedsResolver=true, so the
// embedded authkey/torrent_pass never reaches the feed), Peers=Seeders+Leechers,
// Grabs=Snatched, the category derived from Resolution, PublishDate from the unix
// Time seconds rendered as UTC RFC3339, and IMDBID from ImdbID (canonicalised to
// the "tt"+7-digit feed form; Prowlarr emits it and the PTP sibling already does).
func (d *driver) toRelease(mapKey string, t *btnTorrent) *sortableRelease {
	seeders := t.Seeders.Int64()
	leechers := t.Leechers.Int64()
	rel := &normalizer.Release{
		Title:                t.ReleaseName,
		Link:                 t.DownloadURL,
		InfoHash:             t.InfoHash,
		Categories:           d.categories(t.Resolution),
		Size:                 t.Size.Int64(),
		Grabs:                t.Snatched.Int64(),
		Seeders:              seeders,
		Leechers:             leechers,
		Peers:                seeders + leechers,
		PublishDate:          time.Unix(t.Time.Int64(), 0).UTC().Format(time.RFC3339),
		TVDBID:               t.TvdbID.Int64(),
		RageID:               t.TvrageID.Int64(),
		IMDBID:               formatIMDB(string(t.ImdbID)),
		DownloadVolumeFactor: 1,
		UploadVolumeFactor:   1,
	}
	return &sortableRelease{Release: rel, torrentIDSortKey: t.TorrentID.Int64(), mapKey: mapKey}
}

// formatIMDB renders BTN's digits-only ImdbID ("7252812") as the canonical
// "tt"+7-digit feed form (matching the normalizer and the PTP sibling's local
// formatIMDB). A blank, non-numeric, or zero id yields "" (the field is omitted).
func formatIMDB(imdbID string) string {
	n, err := strconv.ParseInt(strings.TrimSpace(imdbID), 10, 64)
	if err != nil || n == 0 {
		return ""
	}
	return fmt.Sprintf("tt%07d", n)
}

// categories maps a torrent's Resolution string to its newznab category through the
// site caps (Resolution-keyed), keeping only the canonical newznab category and
// discarding the mapper's synthesised 1:1 custom id. An unmapped/blank resolution falls
// back to the TV root (5000), matching Prowlarr (which emits exactly one category).
func (d *driver) categories(resolution string) []int {
	for _, c := range d.Caps.CategoryMap.MapTrackerCatDescToNewznab(resolution) {
		if c < customCatCutoff {
			return []int{c}
		}
	}
	return []int{tvCategory}
}

// releasesOnly unwraps the sort wrappers back to plain releases (the sort key was only
// needed for the deterministic ordering).
func releasesOnly(in []*sortableRelease) []*normalizer.Release {
	out := make([]*normalizer.Release, len(in))
	for i := range in {
		out[i] = in[i].Release
	}
	return out
}
