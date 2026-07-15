package animebytes

import (
	"encoding/json"
	"fmt"
	"html"
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

// Newznab category ids the parser emits directly. AnimeBytes' scrape.php carries no
// numeric tracker category id, so — exactly like Prowlarr's AnimeBytesParser — the
// parser maps a group's GroupName/CategoryName (and, for games/music, the Property
// descriptors) to canonical newznab ids inline rather than through the caps map (the
// caps map exists only for request-side category filtering, which scrape.php does by
// `anime[...]=1` keys, not response-side mapping).
const (
	catTVAnime       = 5070
	catMovies        = 2000
	catBooksComics   = 7000
	catConsole       = 1000
	catConsolePSP    = 1020
	catConsolePS3    = 1080
	catConsolePSVita = 1120
	catConsole3DS    = 1110
	catConsoleNDS    = 1010
	catConsoleOther  = 1090
	catPCGames       = 4050
	catAudio         = 3000
	catAudioMP3      = 3010
	catAudioLossless = 3040
	catAudioOther    = 3050
)

// propertySeparators are the two delimiters AnimeBytes uses inside a torrent Property
// string ("Blu-ray | MKV | h264 / Softsubs (X)"); Prowlarr splits on both.
var propertySeparators = []string{" | ", " / "}

// commonReleaseGroupPrefixes are the Property prefixes that, when the property also
// carries a "(Group)" suffix, identify the release group used as the title prefix
// (Prowlarr CommonReleaseGroupsProperties, matched case-insensitively).
var commonReleaseGroupPrefixes = []string{"softsubs", "hardsubs", "raw", "translated"}

// uploadTimeLayout is AnimeBytes' UploadTime format; Prowlarr parses it AssumeUniversal
// (i.e. as UTC).
const uploadTimeLayout = "2006-01-02 15:04:05"

// response is the scrape.php JSON envelope. Matches is the total hit count (string-or
// -number on the wire, so flexInt). Error carries a server-side failure message; the
// real AB error body uses the lowercase "error" key (Prowlarr's success model tags it
// "Error", but the live envelope is lowercase — the lowercase key matches what we must
// discriminate on).
type response struct {
	Matches flexInt `json:"Matches"`
	Groups  []group `json:"Groups"`
	Error   string  `json:"error"`
}

// group is one scrape result (a series/album/game …) holding one or more torrents. Year
// is string-or-number on the wire (Prowlarr's AllowReadingFromString), so flexInt.
type group struct {
	ID           flexInt           `json:"ID"`
	CategoryName string            `json:"CategoryName"`
	GroupName    string            `json:"GroupName"`
	FullName     string            `json:"FullName"`
	SeriesName   string            `json:"SeriesName"`
	Year         flexInt           `json:"Year"`
	Image        string            `json:"Image"`
	SynonymnsV2  map[string]string `json:"SynonymnsV2"`
	Description  string            `json:"Description"`
	Tags         []string          `json:"Tags"`
	Torrents     []torrent         `json:"Torrents"`
}

// torrent is one downloadable item inside a group. The numeric fields are typed int/long
// in Prowlarr; house convention uses flexInt for all of them so a string-encoded numeric
// (which AB has been seen to emit) never fails the whole-page decode. Link embeds the
// passkey — the served feed routes it through /dl (NeedsResolver=true), so it is never
// logged.
type torrent struct {
	ID                flexInt      `json:"ID"`
	EditionData       *editionData `json:"EditionData"`
	RawDownMultiplier float64      `json:"RawDownMultiplier"`
	RawUpMultiplier   float64      `json:"RawUpMultiplier"`
	Link              string       `json:"Link"`
	Property          string       `json:"Property"`
	Snatched          flexInt      `json:"Snatched"`
	Seeders           flexInt      `json:"Seeders"`
	Leechers          flexInt      `json:"Leechers"`
	Size              flexInt      `json:"Size"`
	FileCount         flexInt      `json:"FileCount"`
	FileList          []file       `json:"FileList"`
	UploadTime        string       `json:"UploadTime"`
}

// editionData carries a torrent's season/episode descriptor (e.g. "Season 1").
type editionData struct {
	EditionTitle string `json:"EditionTitle"`
}

// file is one entry in a torrent's FileList.
type file struct {
	FileName string  `json:"filename"`
	FileSize flexInt `json:"size"`
}

// flexInt unmarshals a JSON string OR number into an int64. AnimeBytes encodes Year as a
// string-or-number (Prowlarr AllowReadingFromString); the other numerics are typed in
// Prowlarr but tolerated as string-or-number here so a strict struct decode never rejects
// the page (mirrors gazelle.flexInt / broadcastthenet.flexString).
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
			return fmt.Errorf("animebytes: decode numeric field: %w", err)
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

// parseReleases decodes a scrape.php body into normalized releases, reproducing
// Prowlarr's AnimeBytesParser. The discriminator order matches Prowlarr exactly: a
// non-empty Error is a failure first; Matches==0 is an empty page; otherwise each group
// is flattened (one release per torrent, primary title only). A malformed body is a parse
// error. The Error text is scrubbed of the configured passkey before it reaches an error
// string (a hostile server could echo the submitted passkey). Releases are sorted by
// PublishDate descending (Prowlarr's terminal OrderByDescending).
func (d *driver) parseReleases(body []byte) ([]*normalizer.Release, error) {
	var resp response
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("animebytes: decode search response: %s: %w", apphttp.DecodeErrorDetail(err, body), search.ErrParseError)
	}
	if strings.TrimSpace(resp.Error) != "" {
		return nil, d.classifyError(resp.Error)
	}
	if resp.Matches.int64() == 0 {
		return nil, nil
	}

	var rels []*normalizer.Release
	for i := range resp.Groups {
		rels = append(rels, d.flattenGroup(&resp.Groups[i])...)
	}
	sortReleases(rels)
	native.TraceReleases(d.Log, d.Def.ID, rels)
	return rels, nil
}

// classifyError maps a non-empty Error envelope to a sentinel. A passkey/credential
// rejection surfaces as login.ErrLoginFailed (so the management UI shows an auth problem,
// not a transient parse failure); anything else is a parse error. The message is scrubbed
// of the configured passkey first.
func (d *driver) classifyError(msg string) error {
	scrubbed := d.Scrub(msg)
	if looksLikeAuthFailure(scrubbed) {
		return fmt.Errorf("animebytes: api error: %s: %w", scrubbed, login.ErrLoginFailed)
	}
	return fmt.Errorf("animebytes: api error: %s: %w", scrubbed, search.ErrParseError)
}

// looksLikeAuthFailure reports whether an AnimeBytes error message indicates a rejected
// credential (passkey/username), so the failure surfaces as a login error.
func looksLikeAuthFailure(msg string) bool {
	lower := strings.ToLower(msg)
	for _, phrase := range []string{"passkey", "username", "credential", "authoriz", "authenticat", "unauthorized"} {
		if strings.Contains(lower, phrase) {
			return true
		}
	}
	return false
}

// flattenGroup turns one group into releases, one per torrent. The FreeleechOnly setting
// drops a torrent whose RawDownMultiplier != 0 (Prowlarr's parser-side filter); the
// TV/DVD/BD Special groups are skipped entirely (they wreck the matcher). The title is
// the primary (main) title only — Prowlarr additionally fans out one release per Japanese
// /Romaji/Alternative synonym, a parity feature deferred here (noted divergence).
func (d *driver) flattenGroup(g *group) []*normalizer.Release {
	freeOnly := freeleechOnly(d.Cfg)
	rels := make([]*normalizer.Release, 0, len(g.Torrents))
	for i := range g.Torrents {
		t := &g.Torrents[i]
		if freeOnly && t.RawDownMultiplier != 0 {
			continue
		}
		if isSkippedSpecial(g.GroupName) {
			continue
		}
		rel, ok := d.toRelease(g, t)
		if ok {
			rels = append(rels, rel)
		}
	}
	return rels
}

// isSkippedSpecial reports whether a group's GroupName is one Prowlarr drops outright
// (TV/DVD/BD Special) because the synthetic titles confuse the *arr matcher.
func isSkippedSpecial(groupName string) bool {
	switch groupName {
	case "TV Special", "DVD Special", "BD Special":
		return true
	default:
		return false
	}
}

// toRelease maps one group×torrent pair to a normalized release. The publish date must
// parse (Prowlarr ParseExact throws otherwise); an unparseable UploadTime drops the
// release rather than failing the whole page.
func (d *driver) toRelease(g *group, t *torrent) (*normalizer.Release, bool) {
	published, err := time.Parse(uploadTimeLayout, strings.TrimSpace(t.UploadTime))
	if err != nil {
		return nil, false
	}
	props := torrentProperties(t.Property)
	seeders := t.Seeders.int64()
	leechers := t.Leechers.int64()
	rel := &normalizer.Release{
		Title:                composeTitle(g, t, props),
		Description:          g.Description,
		Link:                 t.Link,
		Details:              d.detailsURL(t.ID.int64()),
		Poster:               g.Image,
		Categories:           categories(g, props),
		Size:                 t.Size.int64(),
		Files:                t.FileCount.int64(),
		Grabs:                t.Snatched.int64(),
		Seeders:              seeders,
		Leechers:             leechers,
		Peers:                seeders + leechers,
		Year:                 g.Year.int64(),
		Genre:                strings.Join(g.Tags, ", "),
		PublishDate:          published.UTC().Format(time.RFC3339),
		DownloadVolumeFactor: t.RawDownMultiplier,
		UploadVolumeFactor:   t.RawUpMultiplier,
		MinimumRatio:         minimumRatio,
		MinimumSeedTime:      minimumSeedTime(t.Size.int64()),
	}
	return rel, true
}

// detailsURL is Prowlarr's InfoUrl: {base}torrent/{id}/group.
func (d *driver) detailsURL(id int64) string {
	return d.BaseURL + "torrent/" + strconv.FormatInt(id, 10) + "/group"
}

// minimumSeedTime reproduces Prowlarr's AnimeBytes MST: 259200 seconds (72h) plus an
// extra 5 hours (18000 s) per whole GiB of torrent size.
func minimumSeedTime(size int64) int64 {
	const gib = 1024 * 1024 * 1024
	return 259200 + (size/gib)*18000
}

// minimumRatio is the fixed 1 Prowlarr sets for every AnimeBytes release.
const minimumRatio = 1

// freeleechOnly reports whether the freeleech_only toggle is enabled (parser-side filter).
func freeleechOnly(cfg map[string]string) bool {
	switch strings.ToLower(strings.TrimSpace(cfg["freeleech_only"])) {
	case "true", "1", "on", "yes":
		return true
	default:
		return false
	}
}

// sortReleases orders releases by PublishDate descending (Prowlarr's terminal
// OrderByDescending(PublishDate)). UTC RFC3339 sorts lexically in chronological order, so
// a plain string compare is correct; the stable sort keeps group/torrent input order for
// equal timestamps, so the feed is deterministic.
func sortReleases(rels []*normalizer.Release) {
	sort.SliceStable(rels, func(i, j int) bool {
		return rels[i].PublishDate > rels[j].PublishDate
	})
}

// torrentProperties splits a torrent Property string into its ordered, de-duplicated
// descriptor list, HTML-decoding the whole string first and dropping the "Freeleech"
// marker (Prowlarr ExcludedProperties). Order is the insertion order of first
// appearance, matching .NET's de-facto HashSet iteration for these small, removal-free
// sets — which the synthesized title relies on.
func torrentProperties(property string) []string {
	decoded := html.UnescapeString(property)
	seen := map[string]struct{}{}
	out := make([]string, 0, 8)
	for _, part := range splitProperties(decoded) {
		p := strings.TrimSpace(part)
		if p == "" || strings.EqualFold(p, "Freeleech") {
			continue
		}
		key := strings.ToLower(p)
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, p)
	}
	return out
}

// splitProperties splits on every property separator (" | " and " / "), Prowlarr's
// PropertiesSeparator set.
func splitProperties(s string) []string {
	parts := []string{s}
	for _, sep := range propertySeparators {
		next := make([]string, 0, len(parts))
		for _, p := range parts {
			next = append(next, strings.Split(p, sep)...)
		}
		parts = next
	}
	return parts
}
