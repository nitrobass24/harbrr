package gazelle

import (
	"encoding/json"
	"fmt"
	"html"
	"sort"
	"strconv"
	"strings"

	apphttp "github.com/autobrr/harbrr/internal/http"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/dateparse"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/login"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/normalizer"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
	"github.com/autobrr/harbrr/internal/indexer/native"
)

// customCatCutoff bounds the canonical newznab id range. The caps map carries a
// description on every entry, so the mapper synthesises a 1:1 custom category
// (id + CustomCategoryOffset = 100000); the parser keeps only the canonical id and
// discards that synthetic one (mirroring broadcastthenet).
const customCatCutoff = 100000

// defaultCatID is the tracker category id used when a result's Category is null or
// "Select Category" — Prowlarr's MapTrackerCatToNewznab("1") (=Audio) default.
const defaultCatID = "1"

// statusSuccess is the only response status that yields releases; any other status
// (including an empty one) is a non-success body that surfaces an error.
const statusSuccess = "success"

// browseResponse is the ajax.php?action=browse envelope. Status is "success" on a
// good page; Response is nil on an error body. Error carries the server's message
// (used to classify auth failures). currentPage/pages are JSON strings (unused here).
type browseResponse struct {
	Status   string              `json:"status"`
	Error    string              `json:"error"`
	Response *browseResponseBody `json:"response"`
}

// browseResponseBody holds the result groups. Pagination fields are JSON strings.
type browseResponseBody struct {
	CurrentPage flexInt `json:"currentPage"`
	Pages       flexInt `json:"pages"`
	Results     []group `json:"results"`
}

// group is one browse result. A MUSIC group carries nested Torrents (one release per
// torrent); a NON-MUSIC group has Torrents == nil and IS one release using the
// group-level TorrentID/Size/Seeders/Leechers/Category/Snatches/GroupTime fields.
type group struct {
	GroupID     flexInt   `json:"groupId"`
	GroupName   string    `json:"groupName"`
	Artist      string    `json:"artist"`
	GroupYear   flexInt   `json:"groupYear"`
	ReleaseType string    `json:"releaseType"`
	Tags        []string  `json:"tags"`
	Torrents    []torrent `json:"torrents"`

	TorrentID flexInt `json:"torrentId"`
	Size      flexInt `json:"size"`
	Seeders   flexInt `json:"seeders"`
	Leechers  flexInt `json:"leechers"`
	Snatches  flexInt `json:"snatches"`
	Category  *string `json:"category"`
	GroupTime string  `json:"groupTime"`

	IsFreeLeech         bool `json:"isFreeleech"`
	IsNeutralLeech      bool `json:"isNeutralLeech"`
	IsFreeload          bool `json:"isFreeload"`
	IsPersonalFreeLeech bool `json:"isPersonalFreeleech"`
	CanUseToken         bool `json:"canUseToken"`
}

// torrent is one nested torrent inside a MUSIC group. Numerics are JSON strings on the
// wire, so flexInt tolerates string-or-number. Time is a datetime string (parsed UTC).
type torrent struct {
	TorrentID     flexInt `json:"torrentId"`
	Format        string  `json:"format"`
	Encoding      string  `json:"encoding"`
	Media         string  `json:"media"`
	RemasterTitle string  `json:"remasterTitle"`
	RemasterYear  flexInt `json:"remasterYear"`
	HasLog        bool    `json:"hasLog"`
	LogScore      int     `json:"logScore"`
	HasCue        bool    `json:"hasCue"`
	Scene         bool    `json:"scene"`
	Size          flexInt `json:"size"`
	Seeders       flexInt `json:"seeders"`
	Leechers      flexInt `json:"leechers"`
	Snatches      flexInt `json:"snatches"`
	Time          string  `json:"time"`
	Category      *string `json:"category"`

	IsFreeLeech         bool `json:"isFreeleech"`
	IsNeutralLeech      bool `json:"isNeutralLeech"`
	IsFreeload          bool `json:"isFreeload"`
	IsPersonalFreeLeech bool `json:"isPersonalFreeleech"`
	CanUseToken         bool `json:"canUseToken"`
}

// flexInt unmarshals a JSON string OR number into an int64. Gazelle wire-encodes
// Size/Seeders/Leechers/Snatches as JSON STRINGS (Prowlarr long.Parse/int.Parse), but a
// bare number is tolerated too so a strict struct decode never rejects the page.
type flexInt int64

func (n *flexInt) UnmarshalJSON(b []byte) error {
	if len(b) == 0 || string(b) == "null" {
		*n = 0
		return nil
	}
	s := string(b)
	if b[0] == '"' {
		var str string
		if err := json.Unmarshal(b, &str); err != nil {
			return fmt.Errorf("gazelle: decode numeric field: %w", err)
		}
		s = strings.TrimSpace(str)
	}
	if s == "" {
		*n = 0
		return nil
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		*n = 0 // a malformed numeric degrades to 0, never failing the whole page
		return nil
	}
	*n = flexInt(v)
	return nil
}

// int64 returns the decoded value as a plain int64.
func (n flexInt) int64() int64 { return int64(n) }

// parseBrowse decodes a browse body into normalized releases. A non-JSON or malformed
// body is a parse error. A non-"success" status is NOT an empty-page condition here: it
// is classified as a login failure when the error text looks like an auth rejection,
// otherwise a parse error (apikey scrubbed from any surfaced message). On success it
// flattens each group (music: one release per torrent; non-music: the group itself) and
// sorts by PublishDate descending (mirroring Prowlarr's OrderByDescending(PublishDate)).
func (d *driver) parseBrowse(body []byte) ([]*normalizer.Release, error) {
	var resp browseResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("gazelle: decode browse response: %s: %w", apphttp.DecodeErrorDetail(err, body), search.ErrParseError)
	}
	if resp.Status != statusSuccess {
		return nil, d.classifyStatusError(resp.Status, resp.Error)
	}
	if resp.Response == nil {
		return nil, nil
	}

	var rels []*normalizer.Release
	for i := range resp.Response.Results {
		rels = append(rels, d.flattenGroup(&resp.Response.Results[i])...)
	}
	sortReleases(rels)
	native.TraceReleases(d.log, d.def.ID, rels)
	return rels, nil
}

// classifyStatusError maps a non-success status to a login or parse failure. Gazelle
// returns status:"failure" with an error message; a credentials/authorization phrase
// maps to login.ErrLoginFailed, anything else to search.ErrParseError. The message is
// scrubbed of the configured apikey before it reaches the error string.
func (d *driver) classifyStatusError(status, msg string) error {
	scrubbed := d.scrubAPIKey(msg)
	if looksLikeAuthFailure(scrubbed) {
		return fmt.Errorf("gazelle: browse status %q: %s: %w", status, scrubbed, login.ErrLoginFailed)
	}
	return fmt.Errorf("gazelle: browse status %q: %s: %w", status, scrubbed, search.ErrParseError)
}

// looksLikeAuthFailure reports whether a Gazelle error message indicates a rejected
// credential (so the failure is surfaced as a login error rather than a parse error).
func looksLikeAuthFailure(msg string) bool {
	lower := strings.ToLower(msg)
	for _, phrase := range []string{"credential", "api key", "apikey", "authorization", "authenticat", "unauthorized"} {
		if strings.Contains(lower, phrase) {
			return true
		}
	}
	return false
}

// flattenGroup turns one browse group into releases: a MUSIC group (Torrents != nil)
// emits one release per torrent (group × torrent); a NON-MUSIC group (Torrents == nil)
// is itself one release built from the group-level fields.
func (d *driver) flattenGroup(g *group) []*normalizer.Release {
	if g.Torrents == nil {
		return []*normalizer.Release{d.nonMusicRelease(g)}
	}
	rels := make([]*normalizer.Release, 0, len(g.Torrents))
	for i := range g.Torrents {
		rels = append(rels, d.musicRelease(g, &g.Torrents[i]))
	}
	return rels
}

// musicRelease maps a group×torrent pair to a release. Title is the full Gazelle
// composition; Artist/Album(=GroupName)/Year/Genre(=Tags) populate the music fields;
// PublishDate comes from the torrent's datetime; the category comes from the torrent (or
// group) Category, defaulting to Audio.
func (d *driver) musicRelease(g *group, t *torrent) *normalizer.Release {
	free := d.musicFreeleech(t)
	return &normalizer.Release{
		Title:                composeTitle(g, t),
		Link:                 d.downloadLink(t.TorrentID.int64(), d.wantToken(t.CanUseToken, free)),
		Artist:               g.Artist,
		Album:                g.GroupName,
		Year:                 g.GroupYear.int64(),
		Genre:                strings.Join(g.Tags, ", "),
		Categories:           d.categories(t.Category),
		Size:                 t.Size.int64(),
		Grabs:                t.Snatches.int64(),
		Seeders:              t.Seeders.int64(),
		Leechers:             t.Leechers.int64(),
		Peers:                t.Seeders.int64() + t.Leechers.int64(),
		PublishDate:          d.publishDate(t.Time),
		DownloadVolumeFactor: volumeFactor(free),
		UploadVolumeFactor:   d.uploadVolumeFactor(t.IsNeutralLeech, t.IsFreeload),
	}
}

// nonMusicRelease maps a NON-MUSIC group to a single release: Title=GroupName, the
// group-level numerics, PublishDate from GroupTime (unix-or-fuzzy), category defaulting
// to Audio.
func (d *driver) nonMusicRelease(g *group) *normalizer.Release {
	free := d.groupFreeleech(g)
	return &normalizer.Release{
		Title:                html.UnescapeString(g.GroupName),
		Link:                 d.downloadLink(g.TorrentID.int64(), d.wantToken(g.CanUseToken, free)),
		Year:                 g.GroupYear.int64(),
		Categories:           d.categories(g.Category),
		Size:                 g.Size.int64(),
		Grabs:                g.Snatches.int64(),
		Seeders:              g.Seeders.int64(),
		Leechers:             g.Leechers.int64(),
		Peers:                g.Seeders.int64() + g.Leechers.int64(),
		PublishDate:          d.publishDate(g.GroupTime),
		DownloadVolumeFactor: volumeFactor(free),
		UploadVolumeFactor:   d.uploadVolumeFactor(g.IsNeutralLeech, g.IsFreeload),
	}
}

// composeTitle builds the Gazelle music title EXACTLY per Prowlarr's RED/OPS GetTitle:
//
//	{Artist} - {GroupName} ({GroupYear})
//	  [+ " [ReleaseType]" when ReleaseType is set and != "Unknown"]
//	  [+ " [trim(RemasterTitle RemasterYear)]" when RemasterTitle is set]
//	  + " [" + join(flags, " / ") + "]"
//
// where flags = ["{Format} {Encoding}", "{Media}", "Log ({LogScore}%)" if HasLog,
// "Cue" if HasCue]. The whole title is HTML-unescaped.
func composeTitle(g *group, t *torrent) string {
	var b strings.Builder
	if y := g.GroupYear.int64(); y > 0 {
		fmt.Fprintf(&b, "%s - %s (%d)", g.Artist, g.GroupName, y)
	} else {
		fmt.Fprintf(&b, "%s - %s", g.Artist, g.GroupName)
	}
	// Match Prowlarr's IsNotNullOrWhiteSpace checks: a whitespace-only ReleaseType or
	// RemasterTitle must not emit an empty "[ ]" bracket.
	if rt := strings.TrimSpace(g.ReleaseType); rt != "" && rt != "Unknown" {
		fmt.Fprintf(&b, " [%s]", rt)
	}
	if strings.TrimSpace(t.RemasterTitle) != "" {
		remaster := strings.TrimSpace(t.RemasterTitle)
		if y := t.RemasterYear.int64(); y > 0 {
			remaster = strings.TrimSpace(fmt.Sprintf("%s %d", remaster, y))
		}
		fmt.Fprintf(&b, " [%s]", remaster)
	}
	b.WriteString(" [" + strings.Join(titleFlags(t), " / ") + "]")
	return html.UnescapeString(b.String())
}

// titleFlags is the bracketed flag list of a music torrent: format+encoding, media, an
// optional log-score, and an optional cue marker.
func titleFlags(t *torrent) []string {
	flags := []string{
		t.Format + " " + t.Encoding,
		t.Media,
	}
	if t.HasLog {
		flags = append(flags, fmt.Sprintf("Log (%d%%)", t.LogScore))
	}
	if t.HasCue {
		flags = append(flags, "Cue")
	}
	return flags
}

// musicFreeleech reports whether a torrent is effectively freeleech. RED additionally
// counts IsFreeload (Prowlarr RedactedParser); OPS does not. The site is keyed off the
// driver profile.
func (d *driver) musicFreeleech(t *torrent) bool {
	free := t.IsFreeLeech || t.IsNeutralLeech || t.IsPersonalFreeLeech
	if d.profile.site == "redacted" {
		free = free || t.IsFreeload
	}
	return free
}

// groupFreeleech is the NON-MUSIC equivalent of musicFreeleech using group-level flags.
func (d *driver) groupFreeleech(g *group) bool {
	free := g.IsFreeLeech || g.IsNeutralLeech || g.IsPersonalFreeLeech
	if d.profile.site == "redacted" {
		free = free || g.IsFreeload
	}
	return free
}

// uploadVolumeFactor is 0 for neutral-leech (and, RED-only, freeload) torrents, 1
// otherwise — mirroring Prowlarr RedactedParser's UploadVolumeFactor.
func (d *driver) uploadVolumeFactor(neutralLeech, freeload bool) float64 {
	if neutralLeech || (d.profile.site == "redacted" && freeload) {
		return 0
	}
	return 1
}

// volumeFactor maps a freeleech flag to the download volume factor (0 free, 1 paid).
func volumeFactor(free bool) float64 {
	if free {
		return 0
	}
	return 1
}

// wantToken reports whether the download link should carry usetoken=1: the
// use_freeleech_token setting must be enabled AND the torrent must be able to use a token
// AND it must not already be freeleech (Prowlarr: canUseToken = CanUseToken &&
// !isFreeLeech — a token must never be wasted on already-free content).
func (d *driver) wantToken(canUseToken, free bool) bool {
	return d.useFreeleechToken() && canUseToken && !free
}

// useFreeleechToken reports whether the use_freeleech_token checkbox is enabled. harbrr
// stores a checked checkbox as Jackett's "True" sentinel; common truthy spellings are
// accepted too so whatever the management API persists is interpreted consistently.
func (d *driver) useFreeleechToken() bool {
	switch strings.ToLower(strings.TrimSpace(d.cfg["use_freeleech_token"])) {
	case "true", "1", "on", "yes":
		return true
	default:
		return false
	}
}

// downloadLink builds the header-authenticated download URL. usetoken=1 is appended only
// when withToken is true; it is NEVER sent as usetoken=0 (the OPS quirk — and harmless
// for RED), so the param is simply omitted when off.
func (d *driver) downloadLink(torrentID int64, withToken bool) string {
	link := fmt.Sprintf("%sajax.php?action=download&id=%d", d.baseURL, torrentID)
	if withToken {
		link += "&usetoken=1"
	}
	return link
}

// categories maps a result's Category (a description string like "Music"/"Audiobooks")
// to its newznab category through the caps desc map, discarding the synthesised custom
// id. A null Category or one containing "Select Category" defaults to Audio ("1").
func (d *driver) categories(category *string) []int {
	if category == nil || strings.Contains(*category, "Select Category") {
		return canonical(d.caps.CategoryMap.MapTrackerCatToNewznab(defaultCatID))
	}
	if mapped := canonical(d.caps.CategoryMap.MapTrackerCatDescToNewznab(*category)); mapped != nil {
		return mapped
	}
	return canonical(d.caps.CategoryMap.MapTrackerCatToNewznab(defaultCatID))
}

// canonical keeps only the canonical newznab category id, dropping the mapper's
// synthesised 1:1 custom id (>= 100000), so each release carries exactly one category.
func canonical(ids []int) []int {
	for _, id := range ids {
		if id < customCatCutoff {
			return []int{id}
		}
	}
	return nil
}

// publishDate renders a Gazelle time value as UTC RFC3339. It tolerates a music
// torrent's datetime ("2012-04-14 15:57:00"), a non-music unix-seconds string, and a
// fuzzy value ("now") via the date parser. An unparseable value yields the empty string.
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

// scrubAPIKey removes the configured apikey from s so a server echo cannot leak it.
func (d *driver) scrubAPIKey(s string) string {
	if key := strings.TrimSpace(d.cfg["apikey"]); key != "" {
		s = strings.ReplaceAll(s, key, "[redacted]")
	}
	return s
}

// sortReleases orders releases by PublishDate descending to mirror Prowlarr's terminal
// OrderByDescending(o => o.PublishDate) in both the RED and OPS parsers. PublishDate is a
// UTC RFC3339 string, which sorts lexically in chronological order, so a plain string
// comparison is correct. The stable sort preserves group/torrent input order for any tie
// (equal timestamps), keeping the feed deterministic.
func sortReleases(rels []*normalizer.Release) {
	sort.SliceStable(rels, func(i, j int) bool {
		return rels[i].PublishDate > rels[j].PublishDate
	})
}
