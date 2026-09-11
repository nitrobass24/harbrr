package animebytes

import (
	"encoding/json"
	"fmt"
	"html"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	apphttp "github.com/autobrr/harbrr/internal/http"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/login"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/normalizer"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
	"github.com/autobrr/harbrr/internal/indexer/native"
)

// authFailurePhrases are the words an AnimeBytes error message uses for a rejected
// credential (passkey/username), so such a failure surfaces as a login error.
var authFailurePhrases = []string{"passkey", "username", "credential", "authoriz", "authenticat", "unauthorized"}

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

// commonReleaseGroupPrefixes are the Property prefixes that, when the property also
// carries a "(Group)" suffix, identify the release group used as the title prefix
// (Prowlarr CommonReleaseGroupsProperties, matched case-insensitively).
var commonReleaseGroupPrefixes = []string{"softsubs", "hardsubs", "raw", "translated"}

// uploadTimeLayout is AnimeBytes' UploadTime format; Prowlarr parses it AssumeUniversal
// (i.e. as UTC).
const uploadTimeLayout = "2006-01-02 15:04:05"

// response is the scrape.php JSON envelope. Matches is the total hit count (string-or
// -number on the wire, so native.FlexInt). Error carries a server-side failure message; the
// real AB error body uses the lowercase "error" key (Prowlarr's success model tags it
// "Error", but the live envelope is lowercase — the lowercase key matches what we must
// discriminate on).
type response struct {
	Matches native.FlexInt `json:"Matches"`
	Groups  []group        `json:"Groups"`
	Error   string         `json:"error"`
}

// group is one scrape result (a series/album/game …) holding one or more torrents. Year
// is string-or-number on the wire (Prowlarr's AllowReadingFromString), so native.FlexInt.
type group struct {
	ID           native.FlexInt    `json:"ID"`
	CategoryName string            `json:"CategoryName"`
	GroupName    string            `json:"GroupName"`
	FullName     string            `json:"FullName"`
	SeriesName   string            `json:"SeriesName"`
	Year         native.FlexInt    `json:"Year"`
	Image        string            `json:"Image"`
	SynonymnsV2  map[string]string `json:"SynonymnsV2"`
	Description  string            `json:"Description"`
	Tags         []string          `json:"Tags"`
	Torrents     []torrent         `json:"Torrents"`
}

// torrent is one downloadable item inside a group. The numeric fields are typed int/long
// in Prowlarr; house convention uses native.FlexInt for all of them so a string-encoded numeric
// (which AB has been seen to emit) never fails the whole-page decode. Link embeds the
// passkey — the served feed routes it through /dl (NeedsResolver=true), so it is never
// logged.
type torrent struct {
	ID                native.FlexInt `json:"ID"`
	EditionData       *editionData   `json:"EditionData"`
	RawDownMultiplier float64        `json:"RawDownMultiplier"`
	RawUpMultiplier   float64        `json:"RawUpMultiplier"`
	Link              string         `json:"Link"`
	Property          string         `json:"Property"`
	Snatched          native.FlexInt `json:"Snatched"`
	Seeders           native.FlexInt `json:"Seeders"`
	Leechers          native.FlexInt `json:"Leechers"`
	Size              native.FlexInt `json:"Size"`
	FileCount         native.FlexInt `json:"FileCount"`
	FileList          []file         `json:"FileList"`
	UploadTime        string         `json:"UploadTime"`
}

// editionData carries a torrent's season/episode descriptor (e.g. "Season 1").
type editionData struct {
	EditionTitle string `json:"EditionTitle"`
}

// file is one entry in a torrent's FileList.
type file struct {
	FileName string         `json:"filename"`
	FileSize native.FlexInt `json:"size"`
}

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
	if resp.Matches.Int64() == 0 {
		return nil, nil
	}

	var rels []*normalizer.Release
	for i := range resp.Groups {
		rels = append(rels, d.flattenGroup(&resp.Groups[i])...)
	}
	native.SortByPublishDateDesc(rels)
	native.TraceReleases(d.Log, d.Def.ID, rels)
	return rels, nil
}

// classifyError maps a non-empty Error envelope to a sentinel. A passkey/credential
// rejection surfaces as login.ErrLoginFailed (so the management UI shows an auth problem,
// not a transient parse failure); anything else is a parse error. The message is scrubbed
// of the configured passkey first.
func (d *driver) classifyError(msg string) error {
	scrubbed := d.Scrub(msg)
	if native.MentionsAny(scrubbed, authFailurePhrases...) {
		return fmt.Errorf("animebytes: api error: %s: %w", scrubbed, login.ErrLoginFailed)
	}
	return fmt.Errorf("animebytes: api error: %s: %w", scrubbed, search.ErrParseError)
}

// flattenGroup turns one group into releases, one per torrent. The FreeleechOnly setting
// drops a torrent whose RawDownMultiplier != 0 (Prowlarr's parser-side filter); the
// TV/DVD/BD Special groups are skipped entirely (they wreck the matcher). The title is
// the primary (main) title only — Prowlarr additionally fans out one release per Japanese
// /Romaji/Alternative synonym, a parity feature deferred here (noted divergence).
func (d *driver) flattenGroup(g *group) []*normalizer.Release {
	freeOnly := native.CheckboxOn(d.Cfg["freeleech_only"])
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
	return slices.Contains([]string{"TV Special", "DVD Special", "BD Special"}, groupName)
}

// toRelease maps one group×torrent pair to a normalized release. The publish date must
// parse (Prowlarr ParseExact throws otherwise); an unparseable UploadTime drops the
// release rather than failing the whole page.
func (d *driver) toRelease(g *group, t *torrent) (*normalizer.Release, bool) {
	published, err := time.Parse(uploadTimeLayout, strings.TrimSpace(t.UploadTime))
	if err != nil {
		return nil, false
	}
	props := torrentProperties(t)
	seeders := t.Seeders.Int64()
	leechers := t.Leechers.Int64()
	rel := &normalizer.Release{
		Title:                composeTitle(g, t, props),
		Description:          g.Description,
		Link:                 t.Link,
		Details:              d.detailsURL(t.ID.Int64()),
		Poster:               g.Image,
		Categories:           categories(g, props),
		Size:                 t.Size.Int64(),
		Files:                t.FileCount.Int64(),
		Grabs:                t.Snatched.Int64(),
		Seeders:              seeders,
		Leechers:             leechers,
		Peers:                seeders + leechers,
		Year:                 g.Year.Int64(),
		Genre:                strings.Join(g.Tags, ", "),
		PublishDate:          published.UTC().Format(time.RFC3339),
		DownloadVolumeFactor: t.RawDownMultiplier,
		UploadVolumeFactor:   t.RawUpMultiplier,
		MinimumRatio:         minimumRatio,
		MinimumSeedTime:      minimumSeedTime(t.Size.Int64()),
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

// torrentProperties is the descriptor list a release's title is built from: the split,
// de-duplicated Property list with Prowlarr's three post-split transforms applied —
// "BR-DISK" appended for an M2TS torrent, H.265/H.264 rewritten to HEVC/AVC on a
// Blu-ray-disc torrent (RAW / M2TS* / ISO*), and " Remux" appended to a resolution
// property when a file name says Remux.
func torrentProperties(t *torrent) []string {
	props := splitTorrentProperties(t.Property)
	if slices.ContainsFunc(props, isM2TSProperty) {
		props = append(props, "BR-DISK")
	}
	blurayDisk := slices.ContainsFunc(props, isBluRayDiscProperty)
	remux := hasRemuxFile(t.FileList)
	for i, p := range props {
		if blurayDisk {
			p = h265Re.ReplaceAllString(p, "HEVC")
			p = h264Re.ReplaceAllString(p, "AVC")
		}
		if remux && isRemuxResolution(p) {
			p += " Remux"
		}
		props[i] = p
	}
	return props
}

// isM2TSProperty / isBluRayDiscProperty reproduce Prowlarr's ordinal (case-sensitive)
// property probes: an M2TS property adds the "BR-DISK" marker, and RAW / M2TS* / ISO*
// mark the torrent as a Blu-ray disc (which is what turns H.265/H.264 into HEVC/AVC).
func isM2TSProperty(p string) bool { return strings.HasPrefix(p, "M2TS") }

func isBluRayDiscProperty(p string) bool {
	return p == "RAW" || strings.HasPrefix(p, "M2TS") || strings.HasPrefix(p, "ISO")
}

// h265Re / h264Re are Prowlarr's case-insensitive codec rewrites, applied only to a
// Blu-ray-disc torrent's properties.
var (
	h265Re = regexp.MustCompile(`(?i)\bH\.?265\b`)
	h264Re = regexp.MustCompile(`(?i)\bH\.?264\b`)
)

// remuxResolutions are the resolution properties Prowlarr suffixes with " Remux" when
// the torrent's file list names a remux (matched case-insensitively).
var remuxResolutions = []string{"1080i", "1080p", "2160p", "4K"}

// isRemuxResolution reports whether a property is one of the remux-eligible resolutions.
func isRemuxResolution(p string) bool {
	return slices.ContainsFunc(remuxResolutions, func(r string) bool { return strings.EqualFold(p, r) })
}

// hasRemuxFile reports whether any file name contains "Remux" (case-insensitive),
// Prowlarr's trigger for the " Remux" resolution suffix.
func hasRemuxFile(files []file) bool {
	return slices.ContainsFunc(files, func(f file) bool {
		return strings.Contains(strings.ToLower(f.FileName), "remux")
	})
}

// splitTorrentProperties splits a torrent Property string into its ordered, de-duplicated
// descriptor list, HTML-decoding the whole string first and dropping the "Freeleech"
// marker (Prowlarr ExcludedProperties). Order is the insertion order of first
// appearance, matching .NET's de-facto HashSet iteration for these small, removal-free
// sets — which the synthesized title relies on.
func splitTorrentProperties(property string) []string {
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
	return strings.Split(strings.ReplaceAll(s, " / ", " | "), " | ")
}
