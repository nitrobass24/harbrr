package gazellegames

import (
	"encoding/json"
	"fmt"
	"html"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"

	apphttp "github.com/autobrr/harbrr/internal/http"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/dateparse"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/login"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/normalizer"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
)

const (
	// statusSuccess is the only response status that yields releases; any other status
	// (a non-"success" string, or the numeric HTTP code GGn embeds in an error body such
	// as 401) is a non-success body that surfaces an error.
	statusSuccess = "success"

	// torrentTypeWanted keeps only TorrentType=="TORRENT" rows (Prowlarr filters the
	// group's torrent map to TorrentType.ToUpperInvariant() == "TORRENT").
	torrentTypeWanted = "TORRENT"

	// customCatCutoff bounds the canonical newznab id range. The caps map carries a
	// description on every entry, so the mapper synthesises a 1:1 custom category
	// (id + CustomCategoryOffset = 100000); the parser keeps only canonical ids and
	// discards the synthetic ones (mirroring gazelle/broadcastthenet).
	customCatCutoff = 100000

	// minimumSeedTimeSeconds is the fixed MinimumSeedTime Prowlarr sets on every GGn
	// release: 80 hours (3 days + 8 hours).
	minimumSeedTimeSeconds = 288000

	// downloadPath / detailsPath are the torrents.php endpoints Prowlarr rebuilds for the
	// download and info URLs.
	downloadPath = "torrents.php"
	detailsPath  = "torrents.php"

	// downloadAuthKeyDummy is the placeholder authkey Prowlarr passes — GGn requires the
	// param but does not check it (the real authkey is randomly cycled), so a constant is
	// sent and only torrent_pass (the passkey) is the secret.
	downloadAuthKeyDummy = "prowlarr"
)

// yearRegex matches a 4-digit year already present in a title (Prowlarr's YearRegex). It
// gates whether the group Year is appended, so a title that already contains a year is
// left unchanged.
var yearRegex = regexp.MustCompile(`\b(?:19|20|21)\d{2}\b`)

// gazelleGamesResponse is the api.php?request=search envelope. Status is "success" on a
// good page; on a non-success body Status carries a different string or the numeric HTTP
// code (so flexString tolerates string-or-number). Response is the keyed group map on
// success and an arbitrary shape (string/array/missing) otherwise, so it is held as raw
// JSON and only decoded as a map after the success gate. Error carries the server message.
type gazelleGamesResponse struct {
	Status   flexString      `json:"status"`
	Error    string          `json:"error"`
	Response json.RawMessage `json:"response"`
}

// gazelleGamesGroup is one search-result group (the value of the groupId-keyed response
// map). Artists supply the category descriptions; Torrents is the torrentId-keyed torrent
// map on a filled group and an empty array ([]) on an empty one, so it is held as raw JSON
// and decoded as a map only when it is a JSON object. Year is appended to the title.
type gazelleGamesGroup struct {
	Artists  []gazelleGamesArtist `json:"Artists"`
	Torrents json.RawMessage      `json:"Torrents"`
	Year     flexString           `json:"Year"`
}

// gazelleGamesArtist is one artist/platform entry. Its Name is mapped through the
// description-keyed category map (a platform name like "Windows" or a real category).
type gazelleGamesArtist struct {
	ID   flexString `json:"Id"`
	Name string     `json:"Name"`
}

// gazelleGamesTorrent is one torrent inside a group (the value of the torrentId-keyed
// torrent map). Numerics GGn wire-encodes as JSON strings (Size, Snatched can be null) use
// flexString; Time is a "yyyy-MM-dd HH:mm:ss" datetime (parsed UTC). FreeTorrent is a
// string enum (Normal/FreeLeech/Neutral/Either).
type gazelleGamesTorrent struct {
	CategoryID    flexString `json:"CategoryId"`
	Format        string     `json:"Format"`
	Encoding      string     `json:"Encoding"`
	Language      string     `json:"Language"`
	Region        string     `json:"Region"`
	RemasterYear  string     `json:"RemasterYear"`
	RemasterTitle string     `json:"RemasterTitle"`
	ReleaseTitle  string     `json:"ReleaseTitle"`
	Miscellaneous string     `json:"Miscellaneous"`
	Scene         flexString `json:"Scene"`
	Dupable       flexString `json:"Dupable"`
	Time          string     `json:"Time"`
	TorrentType   string     `json:"TorrentType"`
	FileCount     flexString `json:"FileCount"`
	Size          flexString `json:"Size"`
	Snatched      flexString `json:"Snatched"`
	Seeders       flexString `json:"Seeders"`
	Leechers      flexString `json:"Leechers"`
	FreeTorrent   string     `json:"FreeTorrent"`
	LowSeedFL     bool       `json:"LowSeedFL"`
	GameDoxType   string     `json:"GameDOXType"`
}

// flexString unmarshals a JSON string OR number into a string. GGn wire-encodes numerics
// (Size, Seeders, Scene, …) as JSON strings, but a bare number (or null) is tolerated too
// so a strict struct decode never rejects the body (mirrors broadcastthenet flexString).
type flexString string

func (s *flexString) UnmarshalJSON(b []byte) error {
	if len(b) == 0 || string(b) == "null" {
		*s = ""
		return nil
	}
	if b[0] == '"' {
		var str string
		if err := json.Unmarshal(b, &str); err != nil {
			return fmt.Errorf("gazellegames: decode string field: %w", err)
		}
		*s = flexString(str)
		return nil
	}
	*s = flexString(b) // a bare JSON number/bool: keep its literal text
	return nil
}

// int64 parses the flexString as a base-10 int64; a blank or malformed value yields 0 (a
// bad numeric degrades to 0 rather than failing the whole page).
func (s flexString) int64() int64 {
	n, err := strconv.ParseInt(strings.TrimSpace(string(s)), 10, 64)
	if err != nil {
		return 0
	}
	return n
}

// string returns the decoded text.
func (s flexString) string() string { return string(s) }

// parseSearch decodes a search body into normalized releases. A non-JSON or malformed body
// is a parse error. A non-"success" status is classified as a login failure when the
// status/error text looks like an auth rejection, otherwise a parse error (apikey scrubbed
// from any surfaced message). On success it decodes the groupId-keyed group map, flattens
// each filled group's torrentId-keyed torrent map (TorrentType=="TORRENT" only) into one
// release per torrent, and sorts by PublishDate descending (Prowlarr's terminal
// OrderByDescending(PublishDate)).
func (d *driver) parseSearch(body []byte) ([]*normalizer.Release, error) {
	var resp gazelleGamesResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("gazellegames: decode search response: %s: %w", apphttp.DecodeErrorDetail(err, body), search.ErrParseError)
	}
	if resp.Status.string() != statusSuccess {
		return nil, d.classifyStatusError(resp.Status.string(), resp.Error)
	}

	if !isJSONObject(resp.Response) {
		return nil, nil // a non-object response (Prowlarr's "not a JObject" guard): no groups
	}
	groups := map[int64]gazelleGamesGroup{}
	if err := json.Unmarshal(resp.Response, &groups); err != nil {
		return nil, fmt.Errorf("gazellegames: decode group map: %s: %w", apphttp.DecodeErrorDetail(err, resp.Response), search.ErrParseError)
	}

	var rels []*normalizer.Release
	for groupID, g := range groups {
		groupRels, err := d.flattenGroup(groupID, &g)
		if err != nil {
			return nil, err
		}
		rels = append(rels, groupRels...)
	}
	sortReleases(rels)
	return rels, nil
}

// isJSONObject reports whether raw is a non-null JSON object ({…}); GGn sends an array or a
// string for an empty/error response, which must not be decoded as a keyed map.
func isJSONObject(raw json.RawMessage) bool {
	t := strings.TrimSpace(string(raw))
	return strings.HasPrefix(t, "{")
}

// classifyStatusError maps a non-success status to a login or parse failure. A
// credentials/authorization phrase (or a 401/403 numeric status) maps to
// login.ErrLoginFailed, anything else to search.ErrParseError. The status and message are
// scrubbed of the configured apikey before reaching the error string.
func (d *driver) classifyStatusError(status, msg string) error {
	scrubbed := d.scrubAPIKey(msg)
	if looksLikeAuthFailure(status, scrubbed) {
		return fmt.Errorf("gazellegames: search status %q: %s: %w", status, scrubbed, login.ErrLoginFailed)
	}
	return fmt.Errorf("gazellegames: search status %q: %s: %w", status, scrubbed, search.ErrParseError)
}

// looksLikeAuthFailure reports whether a GGn non-success status/message indicates a
// rejected credential (so the failure surfaces as a login error). GGn embeds the HTTP code
// in the body's status for an auth rejection (401/403), and the error text names the key.
func looksLikeAuthFailure(status, msg string) bool {
	switch strings.TrimSpace(status) {
	case "401", "403":
		return true
	}
	lower := strings.ToLower(msg)
	for _, phrase := range []string{"api key", "apikey", "credential", "authorization", "authenticat", "unauthorized", "forbidden"} {
		if strings.Contains(lower, phrase) {
			return true
		}
	}
	return false
}

// flattenGroup turns one group into releases: an empty group (Torrents is [] / not an
// object) emits none; a filled group emits one release per TorrentType=="TORRENT" torrent,
// keyed by torrentId. A Torrents payload that IS a JSON object but does not decode into the
// torrentId-keyed torrent map is a malformed body for a non-empty group (search.ErrParseError),
// not an empty group — dropping it silently would hide a real decode failure.
//
// Categories are computed ONCE per group from the artist names (Prowlarr's group-scoped
// `categories` variable). When that is empty, the fallback is derived from the FIRST emitted
// torrent's CategoryId and then STICKS for the whole group — Prowlarr never re-derives it
// per torrent (categories.Length is no longer 0 after the first fallback). Reproducing that
// sticky behaviour requires iterating the group's torrents in key order, not Go map order,
// so the torrents are visited by ascending torrentId.
func (d *driver) flattenGroup(groupID int64, g *gazelleGamesGroup) ([]*normalizer.Release, error) {
	if !isJSONObject(g.Torrents) {
		return nil, nil
	}
	torrents := map[int64]gazelleGamesTorrent{}
	if err := json.Unmarshal(g.Torrents, &torrents); err != nil {
		return nil, fmt.Errorf("gazellegames: decode torrents for group %d: %s: %w", groupID, apphttp.DecodeErrorDetail(err, g.Torrents), search.ErrParseError)
	}

	cats := d.groupCategories(g)
	rels := make([]*normalizer.Release, 0, len(torrents))
	for _, torrentID := range sortedTorrentIDs(torrents) {
		t := torrents[torrentID]
		if !strings.EqualFold(t.TorrentType, torrentTypeWanted) {
			continue
		}
		if len(cats) == 0 {
			// First emitted torrent in a group with no artist-derived categories seeds the
			// group fallback from its own CategoryId; it then sticks for every later torrent.
			cats = canonical(d.caps.CategoryMap.MapTrackerCatToNewznab(t.CategoryID.string()))
		}
		rels = append(rels, d.toRelease(groupID, torrentID, g, &t, cats))
	}
	return rels, nil
}

// sortedTorrentIDs returns the group's torrent ids in ascending order so the per-group
// category fallback is seeded by a stable, JSON-key-ordered "first" torrent rather than by
// Go's randomised map iteration.
func sortedTorrentIDs(torrents map[int64]gazelleGamesTorrent) []int64 {
	ids := make([]int64, 0, len(torrents))
	for id := range torrents {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

// toRelease maps one group×torrent pair to a release. Link is the rebuilt torrents.php
// download URL (served only through /dl, so the passkey it carries never reaches the
// feed); Details is the torrents.php info page. cats is the group-resolved category set
// (artist-derived, or the group-sticky CategoryId fallback) passed in by flattenGroup.
func (d *driver) toRelease(groupID, torrentID int64, g *gazelleGamesGroup, t *gazelleGamesTorrent, cats []int) *normalizer.Release {
	free := freeleech(t)
	return &normalizer.Release{
		Title:                composeTitle(g, t),
		Link:                 d.downloadURL(torrentID),
		Details:              d.detailsURL(groupID, torrentID),
		Categories:           cats,
		Size:                 t.Size.int64(),
		Files:                t.FileCount.int64(),
		Grabs:                t.Snatched.int64(),
		Seeders:              t.Seeders.int64(),
		Leechers:             t.Leechers.int64(),
		Peers:                t.Seeders.int64() + t.Leechers.int64(),
		PublishDate:          d.publishDate(t.Time),
		DownloadVolumeFactor: downloadVolumeFactor(t, free),
		UploadVolumeFactor:   uploadVolumeFactor(t),
		MinimumSeedTime:      minimumSeedTimeSeconds,
	}
}

// groupCategories maps a group's artist names through the description-keyed category map,
// de-duplicated and reduced to canonical newznab ids. A platform name not in the (deferred)
// platform map yields nothing here, so the per-torrent CategoryId fallback applies.
func (d *driver) groupCategories(g *gazelleGamesGroup) []int {
	seen := map[int]struct{}{}
	var out []int
	for _, a := range g.Artists {
		for _, id := range canonical(d.caps.CategoryMap.MapTrackerCatDescToNewznab(a.Name)) {
			if _, ok := seen[id]; ok {
				continue
			}
			seen[id] = struct{}{}
			out = append(out, id)
		}
	}
	return out
}

// composeTitle builds the GGn title EXACTLY per Prowlarr's GazelleGamesParser.GetTitle:
//
//	HtmlDecode(ReleaseTitle)
//	  [+ " ({Year})" when group.Year>0 and the title has no 4-digit year already]
//	  [+ " [trim(HtmlDecode(RemasterTitle) RemasterYear)]" when RemasterTitle is set]
//	  [+ " [" + join(flags, " / ") + "]" when any flag is non-blank]
//	  [+ " [{GameDoxType}]" when GameDoxType is set]
//
// where flags = ["{Format} {Encoding}", join(artist names, ", "), Language, Region,
// Miscellaneous, "Trumpable" if Dupable==1], keeping only the non-blank entries.
func composeTitle(g *gazelleGamesGroup, t *gazelleGamesTorrent) string {
	title := html.UnescapeString(t.ReleaseTitle)
	if g.Year.int64() > 0 && strings.TrimSpace(title) != "" && !yearRegex.MatchString(title) {
		title += fmt.Sprintf(" (%d)", g.Year.int64())
	}
	if strings.TrimSpace(t.RemasterTitle) != "" {
		title += fmt.Sprintf(" [%s]", strings.TrimSpace(html.UnescapeString(t.RemasterTitle)+" "+t.RemasterYear))
	}
	if flags := titleFlags(g, t); len(flags) > 0 {
		title += " [" + strings.Join(flags, " / ") + "]"
	}
	if gd := strings.TrimSpace(t.GameDoxType); gd != "" {
		title += fmt.Sprintf(" [%s]", gd)
	}
	return title
}

// titleFlags is the bracketed flag list: format+encoding, the joined artist names,
// language, region, miscellaneous, and a Trumpable marker — keeping only non-blank entries
// (Prowlarr's flags.Where(IsNotNullOrWhiteSpace)).
func titleFlags(g *gazelleGamesGroup, t *gazelleGamesTorrent) []string {
	candidates := []string{
		strings.TrimSpace(t.Format + " " + t.Encoding),
		joinArtists(g.Artists),
		t.Language,
		t.Region,
		t.Miscellaneous,
	}
	if t.Dupable.int64() == 1 {
		candidates = append(candidates, "Trumpable")
	}
	flags := make([]string, 0, len(candidates))
	for _, c := range candidates {
		if strings.TrimSpace(c) != "" {
			flags = append(flags, c)
		}
	}
	return flags
}

// joinArtists joins the group's artist names with ", " (Prowlarr's
// group.Artists.Select(a => a.Name).Join(", ")); an empty list yields "".
func joinArtists(artists []gazelleGamesArtist) string {
	if len(artists) == 0 {
		return ""
	}
	names := make([]string, len(artists))
	for i, a := range artists {
		names[i] = a.Name
	}
	return strings.Join(names, ", ")
}

// freeleech reports whether a torrent's FreeTorrent enum is FreeLeech or Neutral (the two
// download-free states), matching Prowlarr's Enum.TryParse(FreeTorrent) against the
// FreeLeech/Neutral members. The comparison is case-insensitive.
func freeleech(t *gazelleGamesTorrent) bool {
	switch strings.ToLower(strings.TrimSpace(t.FreeTorrent)) {
	case "freeleech", "neutral":
		return true
	default:
		return false
	}
}

// downloadVolumeFactor is 0 when the torrent is freeleech/neutral OR LowSeedFL (low-seed
// freeleech), 1 otherwise — Prowlarr's downloadVolumeFactor.
func downloadVolumeFactor(t *gazelleGamesTorrent, free bool) float64 {
	if free || t.LowSeedFL {
		return 0
	}
	return 1
}

// uploadVolumeFactor is 0 only for a Neutral torrent, 1 otherwise. Prowlarr keys this off
// the Neutral enum member specifically; freeleech alone keeps the upload factor at 1.
func uploadVolumeFactor(t *gazelleGamesTorrent) float64 {
	if strings.EqualFold(strings.TrimSpace(t.FreeTorrent), "neutral") {
		return 0
	}
	return 1
}

// downloadURL rebuilds Prowlarr's GetDownloadUrl: {base}torrents.php?action=download&id=
// {torrentId}&authkey=prowlarr&torrent_pass={passkey}. The passkey is a secret; this URL is
// served only through /dl (the proxy keeps it out of the feed) and is never logged.
func (d *driver) downloadURL(torrentID int64) string {
	params := url.Values{}
	params.Set("action", "download")
	params.Set("id", strconv.FormatInt(torrentID, 10))
	params.Set("authkey", downloadAuthKeyDummy)
	params.Set("torrent_pass", strings.TrimSpace(d.cfgValue("passkey")))
	return d.baseURL + downloadPath + "?" + params.Encode()
}

// detailsURL rebuilds Prowlarr's GetInfoUrl: {base}torrents.php?id={groupId}&torrentid=
// {torrentId}.
func (d *driver) detailsURL(groupID, torrentID int64) string {
	params := url.Values{}
	params.Set("id", strconv.FormatInt(groupID, 10))
	params.Set("torrentid", strconv.FormatInt(torrentID, 10))
	return d.baseURL + detailsPath + "?" + params.Encode()
}

// publishDate renders a GGn time value as UTC RFC3339. It tolerates the "yyyy-MM-dd
// HH:mm:ss" datetime GGn emits and a fuzzy value via the date parser. An unparseable value
// yields the empty string.
func (d *driver) publishDate(value string) string {
	if strings.TrimSpace(value) == "" {
		return ""
	}
	out, err := dateparse.New(dateparse.WithClock(d.clock)).ParseRelTime(value)
	if err != nil {
		return ""
	}
	return out
}

// canonical keeps only the canonical newznab category ids, dropping the mapper's
// synthesised 1:1 custom ids (>= 100000) so a release carries only real categories.
func canonical(ids []int) []int {
	var out []int
	for _, id := range ids {
		if id < customCatCutoff {
			out = append(out, id)
		}
	}
	return out
}

// scrubAPIKey removes the configured apikey from s so a server echo cannot leak it.
func (d *driver) scrubAPIKey(s string) string {
	if key := strings.TrimSpace(d.cfgValue("apikey")); key != "" {
		s = strings.ReplaceAll(s, key, "[redacted]")
	}
	return s
}

// sortReleases orders releases by PublishDate descending (Prowlarr's terminal
// OrderByDescending(PublishDate)). PublishDate is a UTC RFC3339 string, which sorts
// lexically in chronological order, so a plain string comparison is correct; ties break on
// the rebuilt download Link so the map-iteration order does not make the feed
// non-deterministic.
func sortReleases(rels []*normalizer.Release) {
	sort.SliceStable(rels, func(i, j int) bool {
		if rels[i].PublishDate != rels[j].PublishDate {
			return rels[i].PublishDate > rels[j].PublishDate
		}
		return rels[i].Link < rels[j].Link
	})
}
