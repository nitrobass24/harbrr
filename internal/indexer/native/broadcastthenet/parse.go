package broadcastthenet

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/login"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/normalizer"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
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
// JSON string, so flexString decodes it). Torrents is kept as RawMessage because PHP
// serializes an empty associative array as a JSON ARRAY (`[]`) rather than an object
// (`{}`): decoding it straight into a map would fail the struct decode on a zero-result
// page, so the array wire form is tolerated and only an object (`{…}`) is unmarshalled
// into the id→torrent map (cf. Prowlarr, which short-circuits on a zero count).
type btnResult struct {
	Results  flexString      `json:"results"`
	Torrents json.RawMessage `json:"torrents"`
}

// btnError is the JSON-RPC error object.
type btnError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// btnTorrent is one torrent row. BTN wire-encodes EVERY field as a JSON string,
// including the numerics, so every numeric uses flexString and is parsed at mapping
// time (mirroring myanonamouse's tolerant decode and Prowlarr's JSON converter).
type btnTorrent struct {
	TorrentID   flexString `json:"TorrentID"`
	GroupID     flexString `json:"GroupID"`
	ReleaseName string     `json:"ReleaseName"`
	Category    string     `json:"Category"`
	Resolution  string     `json:"Resolution"`
	Origin      string     `json:"Origin"`
	Size        flexString `json:"Size"`
	Time        flexString `json:"Time"`
	Snatched    flexString `json:"Snatched"`
	Seeders     flexString `json:"Seeders"`
	Leechers    flexString `json:"Leechers"`
	TvdbID      flexString `json:"TvdbID"`
	TvrageID    flexString `json:"TvrageID"`
	ImdbID      flexString `json:"ImdbID"`
	InfoHash    string     `json:"InfoHash"`
	DownloadURL string     `json:"DownloadURL"`
}

// flexString unmarshals a JSON string OR number into a string. BTN sends every field
// (incl. numerics like TorrentID, Size, Seeders) as a JSON string, but the count field
// and a hardened decode tolerate a bare number too — so a strict struct decode never
// rejects the body (cf. myanonamouse mamFlexString).
type flexString string

func (s *flexString) UnmarshalJSON(b []byte) error {
	if len(b) == 0 || string(b) == "null" {
		*s = ""
		return nil
	}
	if b[0] == '"' {
		var str string
		if err := json.Unmarshal(b, &str); err != nil {
			return fmt.Errorf("broadcastthenet: decode string field: %w", err)
		}
		*s = flexString(str)
		return nil
	}
	*s = flexString(b) // a bare JSON number: keep its literal text
	return nil
}

// int64 parses the flexString as a base-10 int64; a blank or unparseable value yields 0
// (BTN sends "0" for absent numerics, and a malformed numeric must not fail the whole
// page — it degrades to 0, matching the tolerant decode the contract requires).
func (s flexString) int64() int64 {
	n, err := strconv.ParseInt(strings.TrimSpace(string(s)), 10, 64)
	if err != nil {
		return 0
	}
	return n
}

// parseReleases decodes a getTorrents JSON-RPC body into normalized releases. It checks
// the error envelope first: a -32001 ("Invalid API Key") maps to login.ErrLoginFailed,
// any other JSON-RPC error (or a null result) is a parse error with the apikey scrubbed.
// The torrents map iterates in an unspecified order, so releases are sorted by numeric
// TorrentID for a deterministic feed (and stable tests).
func (d *driver) parseReleases(body []byte) ([]*normalizer.Release, error) {
	var resp btnResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("broadcastthenet: decode search response: %w", search.ErrParseError)
	}
	if resp.Error != nil {
		if resp.Error.Code == invalidAPIKeyCode {
			return nil, fmt.Errorf("broadcastthenet: %s: %w", d.scrubAPIKey(resp.Error.Message), login.ErrLoginFailed)
		}
		return nil, fmt.Errorf("broadcastthenet: api error %d: %s: %w",
			resp.Error.Code, d.scrubAPIKey(resp.Error.Message), search.ErrParseError)
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
	return releasesOnly(releases), nil
}

// decodeTorrents resolves the result's torrents field into the id→torrent map. A
// zero-result page is short-circuited (no torrents). PHP serializes an empty associative
// array as `[]`, so the field is only unmarshalled when it begins with '{' (a JSON
// object); any other shape (the `[]` array form, null, absent) is treated as zero
// torrents rather than a decode failure.
func decodeTorrents(result *btnResult) (map[string]btnTorrent, error) {
	empty := map[string]btnTorrent{}
	if result.Results.int64() == 0 {
		return empty, nil
	}
	raw := bytes.TrimSpace(result.Torrents)
	if len(raw) == 0 || raw[0] != '{' {
		return empty, nil
	}
	var torrents map[string]btnTorrent
	if err := json.Unmarshal(raw, &torrents); err != nil {
		return nil, fmt.Errorf("broadcastthenet: decode torrents: %w", search.ErrParseError)
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
// Grabs=Snatched, the category derived from Resolution, and PublishDate from the unix
// Time seconds rendered as UTC RFC3339.
func (d *driver) toRelease(mapKey string, t *btnTorrent) *sortableRelease {
	seeders := t.Seeders.int64()
	leechers := t.Leechers.int64()
	rel := &normalizer.Release{
		Title:                t.ReleaseName,
		Link:                 t.DownloadURL,
		InfoHash:             t.InfoHash,
		Categories:           d.categories(t.Resolution),
		Size:                 t.Size.int64(),
		Grabs:                t.Snatched.int64(),
		Seeders:              seeders,
		Leechers:             leechers,
		Peers:                seeders + leechers,
		PublishDate:          time.Unix(t.Time.int64(), 0).UTC().Format(time.RFC3339),
		TVDBID:               t.TvdbID.int64(),
		RageID:               t.TvrageID.int64(),
		DownloadVolumeFactor: 1,
		UploadVolumeFactor:   1,
	}
	return &sortableRelease{Release: rel, torrentIDSortKey: t.TorrentID.int64(), mapKey: mapKey}
}

// categories maps a torrent's Resolution string to its newznab category through the
// site caps (Resolution-keyed), keeping only the canonical newznab category and
// discarding the mapper's synthesised 1:1 custom id. An unmapped/blank resolution falls
// back to the TV root (5000), matching Prowlarr (which emits exactly one category).
func (d *driver) categories(resolution string) []int {
	for _, c := range d.caps.CategoryMap.MapTrackerCatDescToNewznab(resolution) {
		if c < customCatCutoff {
			return []int{c}
		}
	}
	return []int{tvCategory}
}

// scrubAPIKey removes the configured API key from s so a server echo (e.g. in an error
// message) cannot leak it. Mirrors filelist.scrubPasskey; the apikey is the body's first
// positional param, never logged, but an error string is scrubbed defensively.
func (d *driver) scrubAPIKey(s string) string {
	if key := strings.TrimSpace(d.cfg["apikey"]); key != "" {
		s = strings.ReplaceAll(s, key, "[redacted]")
	}
	return s
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
