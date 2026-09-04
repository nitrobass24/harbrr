package gazelle

import (
	"fmt"
	"net/url"
	"strconv"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/loader"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/normalizer"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
	"github.com/autobrr/harbrr/internal/indexer/native"
)

// siteConfig is one Gazelle site's entire behavioral declaration (ADR 0003): an
// authStrategy plus the data and optional quirk hooks the shared auth/parse/search/grab
// code reads instead of branching on a site id. New resolves it from siteConfigs by
// definition id.
type siteConfig struct {
	strategy authStrategy
	// sessionCookieSetting is the settings-store name a form-login site persists its
	// session cookie under; empty for an apiKeyAuth site, which carries no session.
	sessionCookieSetting string
	// classify is the status dialect handed to Base.Do/DoDownload for this site.
	classify native.Classify
	// disableRedirects, when true, disables redirect following so an expired
	// form-login session's redirect-to-login-page surfaces as a classified status
	// instead of being silently followed.
	disableRedirects bool
	// downloadViaAjax routes the download link through ajax.php instead of the
	// inherited torrents.php. It mirrors Prowlarr's base/override relationship:
	// GazelleParser.GetDownloadUrl (protected virtual — what every plain Gazelle
	// indexer inherits) builds torrents.php?action=download, and only Redacted.cs and
	// Orpheus.cs override it with a private GetDownloadUrl on ajax.php, because
	// ajax.php is the API-key surface. So this flag pairs with apiKeyAuth; a
	// form-login/session site must leave it false or its download 500s (#424).
	downloadViaAjax bool
	// pageSize is the site's fixed upstream page size; 0 means the site has no
	// upstream paging (RED/OPS return everything matching in one browse call).
	pageSize        int
	minimumRatio    float64
	minimumSeedTime int64
	// countsFreeloadAsFree mirrors Prowlarr's RedactedParser: RED (only) additionally
	// treats IsFreeload as freeleech for both the download-volume factor and the
	// freeleech-token guard.
	countsFreeloadAsFree bool
	// buildQuery, when set, adds the site's extra browse query parameters (AlphaRatio's
	// freeleech/scene/IMDB/page params) on top of the shared RED/OPS parameter set.
	buildQuery func(d *driver, q search.Query, page int, params url.Values)
	// parseProfile, when set, applies release-shaping quirks a site needs beyond the
	// shared mapping (AlphaRatio's Details link, IMDB tag, and file count).
	parseProfile func(d *driver, release *normalizer.Release, groupID, torrentID, fileCount int64, tags []string)
}

// alphaRatioCookieSetting is AlphaRatio's persisted-session setting name — data for its
// siteConfig entry below, not shared vocabulary (the planned #28-#31 sites declare their
// own, even if they happen to reuse this name).
const alphaRatioCookieSetting = "cookie"

// brokenStonesCookieSetting is BrokenStones' persisted-session setting name (#31) — its
// own siteConfig data, happening to reuse AlphaRatio's "cookie" name.
const brokenStonesCookieSetting = "cookie"

// siteConfigs is the Gazelle family's data table: one entry per site, keyed by
// definition id. Adding a site (the planned #28-#31) is a table entry here — never an
// edit to auth.go/parse.go/search.go/grab.go.
var siteConfigs = map[string]siteConfig{
	"redacted": {
		strategy:             apiKeyAuth{},
		classify:             native.ClassifyAuth403,
		downloadViaAjax:      true,
		countsFreeloadAsFree: true,
	},
	"orpheus": {
		strategy:        apiKeyAuth{prefix: "token "},
		classify:        native.ClassifyAuth403,
		downloadViaAjax: true,
	},
	"alpharatio": {
		strategy:             formLoginAuth{},
		sessionCookieSetting: alphaRatioCookieSetting,
		classify:             classifyFormLogin,
		disableRedirects:     true,
		pageSize:             50,
		minimumRatio:         1,
		minimumSeedTime:      259200,
		buildQuery:           alphaRatioBuildQuery,
		parseProfile:         alphaRatioParseProfile,
	},
	// brokenstones (#31) is a plain GazelleBase<GazelleSettings> in Prowlarr — no
	// AlphaRatio-only settings (FreeleechOnly/ExcludeScene), no pagination override, and
	// no download-URL override — so it reuses formLoginAuth for username/password login
	// plus the shared browse path, and inherits the base torrents.php download URL
	// (buildQuery/parseProfile nil, pageSize/downloadViaAjax at their zero values). Its
	// ajax.php answers 500 for a cookie session, which is what #424 was.
	"brokenstones": {
		strategy:             formLoginAuth{},
		sessionCookieSetting: brokenStonesCookieSetting,
		classify:             classifyFormLogin,
		disableRedirects:     true,
	},
}

// alphaRatioBuildQuery adds AlphaRatio's browse query parameters on top of the shared
// RED/OPS set: the IMDB tag filter, the freeleech/scene toggles, and the fixed-page
// paging param.
func alphaRatioBuildQuery(d *driver, q search.Query, page int, params url.Values) {
	// AlphaRatio stores imdb ids as "tt#######" tags (parse.go's imdbTag mirrors this).
	// The torznab imdbid param arrives as bare digits, so it must be rendered as the
	// full form — Jackett normalizes via GetFullImdbId before its GazelleTracker sets
	// taglist — or the tag filter matches nothing.
	if imdbID := native.CanonicalIMDBID(q.IMDBID); imdbID != "" {
		params.Set("taglist", imdbID)
	}
	if native.CheckboxOn(d.Cfg["freeleech_only"]) {
		params.Set("freetorrent", "1")
	}
	if native.CheckboxOn(d.Cfg["exclude_scene"]) {
		params.Set("scene", "0")
	}
	if page > 1 {
		params.Set("page", strconv.Itoa(page))
	}
}

// alphaRatioParseProfile applies AlphaRatio's release-shaping quirks: the Details link
// to the group/torrent page, the IMDB id recovered from the tag list, and the file
// count (only present for non-music groups; fileCount is 0 for a music release).
func alphaRatioParseProfile(d *driver, release *normalizer.Release, groupID, torrentID, fileCount int64, tags []string) {
	release.Details = fmt.Sprintf("%storrents.php?id=%d&torrentid=%d", d.BaseURL, groupID, torrentID)
	release.IMDBID = imdbTag(tags)
	release.Files = fileCount
}

// Per-site between-request pacing. autobrr's token buckets are the burst ceilings
// (RED 10 req/10s, OPS 5 req/10s); the steady per-site delay derived from those is
// RED ~1s and OPS ~2s. It rides on the definition's RequestDelay so the registry's
// existing paced client enforces it (no special-casing). Prowlarr itself uses a flat
// 3s for both — these are more permissive but stay within autobrr's measured limits.
const (
	redactedDelaySeconds   = 1.0
	orpheusDelaySeconds    = 2.0
	alphaRatioDelaySeconds = 3.0
	// brokenStonesDelaySeconds is a conservative default in the absence of a published
	// rate limit (Prowlarr's BrokenStones doesn't override GazelleBase's RateLimit).
	brokenStonesDelaySeconds = 2.0
)

// Families returns the Gazelle-family sites as native families. Each carries a
// Go-built, caps-only definition and the shared New factory; per-site auth and parsing
// behavior is keyed by definition id inside the driver.
//
// The RED/OPS settings: apikey is text-typed but its name carries the "apikey" token,
// so harbrr's secret store auto-classifies it as a secret (encrypted at rest, redacted
// by the API) — matching Prowlarr's PrivacyLevel.ApiKey. use_freeleech_token is a
// checkbox toggle that adds &usetoken=1 to the download URL.
func Families() []native.Family {
	return []native.Family{
		{Definition: native.Site{
			ID: "redacted", Name: "Redacted", Link: "https://redacted.sh/",
			Driver: "Gazelle-family", DelaySeconds: redactedDelaySeconds,
			Settings: []loader.SettingsField{native.FieldAPIKey, native.FieldUseFreeleechToken},
			Caps:     gazelleCaps(),
		}.Definition(), Factory: New},
		{Definition: native.Site{
			ID: "orpheus", Name: "Orpheus", Link: "https://orpheus.network/",
			Driver: "Gazelle-family", DelaySeconds: orpheusDelaySeconds,
			Settings: []loader.SettingsField{native.FieldAPIKey, native.FieldUseFreeleechToken},
			Caps:     gazelleCaps(),
		}.Definition(), Factory: New},
		{Definition: alphaRatioDef(), Factory: New},
		{Definition: brokenStonesDef(), Factory: New},
	}
}

func alphaRatioDef() *loader.Definition {
	return native.Site{
		ID: "alpharatio", Name: "AlphaRatio", Link: "https://alpharatio.cc/",
		Driver: "Gazelle-family", DelaySeconds: alphaRatioDelaySeconds,
		Settings: alphaRatioSettings(),
		Caps:     alphaRatioCaps(),
	}.Definition()
}

// alphaRatioSettings are the user-entered fields: required username/password for form
// login (Required is an AlphaRatio spelling the kit fields don't carry, so those two
// stay inline literals) plus the freeleech and scene toggles.
func alphaRatioSettings() []loader.SettingsField {
	return []loader.SettingsField{
		{Name: "username", Label: "Username", Type: "text", Required: true},
		{Name: "password", Label: "Password", Type: "password", Required: true},
		native.FieldUseFreeleechToken,
		native.FieldFreeleechOnly,
		{Name: "exclude_scene", Label: "Exclude scene releases", Type: "checkbox"},
	}
}

func alphaRatioCaps() loader.Caps {
	return loader.Caps{
		CategoryMappings: native.Cats(
			native.Cat{ID: "1", Newznab: "TV/SD", Desc: "TvSD"},
			native.Cat{ID: "2", Newznab: "TV/HD", Desc: "TvHD"},
			native.Cat{ID: "3", Newznab: "TV/UHD", Desc: "TvUHD"},
			native.Cat{ID: "4", Newznab: "TV/SD", Desc: "TvDVDRip"},
			native.Cat{ID: "5", Newznab: "TV/SD", Desc: "TvPackSD"},
			native.Cat{ID: "6", Newznab: "TV/HD", Desc: "TvPackHD"},
			native.Cat{ID: "7", Newznab: "TV/UHD", Desc: "TvPackUHD"},
			native.Cat{ID: "8", Newznab: "Movies/SD", Desc: "MovieSD"},
			native.Cat{ID: "9", Newznab: "Movies/HD", Desc: "MovieHD"},
			native.Cat{ID: "10", Newznab: "Movies/UHD", Desc: "MovieUHD"},
			native.Cat{ID: "11", Newznab: "Movies/SD", Desc: "MoviePackSD"},
			native.Cat{ID: "12", Newznab: "Movies/HD", Desc: "MoviePackHD"},
			native.Cat{ID: "13", Newznab: "Movies/UHD", Desc: "MoviePackUHD"},
			native.Cat{ID: "14", Newznab: "XXX", Desc: "MovieXXX"},
			native.Cat{ID: "15", Newznab: "Movies/BluRay", Desc: "Bluray"},
			native.Cat{ID: "16", Newznab: "TV/Anime", Desc: "AnimeSD"},
			native.Cat{ID: "17", Newznab: "TV/Anime", Desc: "AnimeHD"},
			native.Cat{ID: "18", Newznab: "PC/Games", Desc: "GamesPC"},
			native.Cat{ID: "19", Newznab: "Console/XBox", Desc: "GamesxBox"},
			native.Cat{ID: "20", Newznab: "Console/PS4", Desc: "GamesPS"},
			native.Cat{ID: "21", Newznab: "Console/Wii", Desc: "GamesNin"},
			native.Cat{ID: "22", Newznab: "PC/0day", Desc: "AppsWindows"},
			native.Cat{ID: "23", Newznab: "PC/Mac", Desc: "AppsMAC"},
			native.Cat{ID: "24", Newznab: "PC/0day", Desc: "AppsLinux"},
			native.Cat{ID: "25", Newznab: "PC/Mobile-Other", Desc: "AppsMobile"},
			native.Cat{ID: "26", Newznab: "XXX", Desc: "0dayXXX"},
			native.Cat{ID: "27", Newznab: "Books", Desc: "eBook"},
			native.Cat{ID: "28", Newznab: "Audio/Audiobook", Desc: "AudioBook"},
			native.Cat{ID: "29", Newznab: "Audio/Other", Desc: "Music"},
			native.Cat{ID: "30", Newznab: "Other", Desc: "Misc"},
		),
		Modes: loader.Modes{
			Search:      []string{"q"},
			MovieSearch: []string{"q", "imdbid"},
			TVSearch:    []string{"q", "season", "ep"},
		},
	}
}

// brokenStonesDef builds BrokenStones' (#31) caps-only definition. Source: Prowlarr's
// src/NzbDrone.Core/Indexers/Definitions/BrokenStones.cs (GazelleBase<GazelleSettings>,
// IndexerUrls: https://brokenstones.is/).
func brokenStonesDef() *loader.Definition {
	return native.Site{
		ID: "brokenstones", Name: "BrokenStones", Link: "https://brokenstones.is/",
		Driver: "Gazelle-family", DelaySeconds: brokenStonesDelaySeconds,
		Settings: brokenStonesSettings(),
		Caps:     brokenStonesCaps(),
	}.Definition()
}

// brokenStonesSettings are the user-entered fields: username/password (form login, per
// ADR 0003; Required is a spelling the kit fields don't carry, so both stay inline
// literals) plus the use_freeleech_token checkbox every GazelleSettings site carries.
// Unlike AlphaRatio, Prowlarr's BrokenStones has no FreeleechOnly/ExcludeScene fields.
func brokenStonesSettings() []loader.SettingsField {
	return []loader.SettingsField{
		{Name: "username", Label: "Username", Type: "text", Required: true},
		{Name: "password", Label: "Password", Type: "password", Required: true},
		native.FieldUseFreeleechToken,
	}
}

// brokenStonesCaps mirrors Prowlarr's BrokenStones.SetCapabilities category mapping
// exactly (a MacOS/iOS apps-and-games tracker, not RED/OPS's music categories). Prowlarr
// sets no Tv/Movie/Music search params for this site, so only basic q search applies.
func brokenStonesCaps() loader.Caps {
	return loader.Caps{
		CategoryMappings: native.Cats(
			native.Cat{ID: "1", Newznab: "PC/Mac", Desc: "MacOS Apps"},
			native.Cat{ID: "2", Newznab: "PC/Mac", Desc: "MacOS Games"},
			native.Cat{ID: "3", Newznab: "PC/Mobile-iOS", Desc: "iOS Apps"},
			native.Cat{ID: "4", Newznab: "PC/Mobile-iOS", Desc: "iOS Games"},
			native.Cat{ID: "5", Newznab: "Other", Desc: "Graphics"},
			native.Cat{ID: "6", Newznab: "Audio", Desc: "Audio"},
			native.Cat{ID: "7", Newznab: "Other", Desc: "Tutorials"},
			native.Cat{ID: "8", Newznab: "Other", Desc: "Other"},
		),
		Modes: loader.Modes{
			Search: []string{"q"},
		},
	}
}

// gazelleCaps is the Gazelle (RED/OPS) capability document, identical for both sites
// per Prowlarr's RED.cs / Orpheus.cs SetCapabilities. The category map keys the
// tracker's numeric category id to its newznab category AND the tracker's category
// DESCRIPTION (so a browse result's textual Category — "Music", "Audiobooks", … —
// maps via MapTrackerCatDescToNewznab): 1->Audio("Music"), 2->PC("Applications"),
// 3->Books/EBook("E-Books"), 4->Audio/Audiobook("Audiobooks"), 5->Other("E-Learning
// Videos"), 6->Other("Comedy"), 7->Books/Comics("Comics"). The search modes mirror
// RED/OPS MusicSearchParams (q/artist/album/year — no label) plus basic q and book q.
func gazelleCaps() loader.Caps {
	return loader.Caps{
		CategoryMappings: native.Cats(
			native.Cat{ID: "1", Newznab: "Audio", Desc: "Music"},
			native.Cat{ID: "2", Newznab: "PC", Desc: "Applications"},
			native.Cat{ID: "3", Newznab: "Books/EBook", Desc: "E-Books"},
			native.Cat{ID: "4", Newznab: "Audio/Audiobook", Desc: "Audiobooks"},
			native.Cat{ID: "5", Newznab: "Other", Desc: "E-Learning Videos"},
			native.Cat{ID: "6", Newznab: "Other", Desc: "Comedy"},
			native.Cat{ID: "7", Newznab: "Books/Comics", Desc: "Comics"},
		),
		Modes: loader.Modes{
			Search:      []string{"q"},
			MusicSearch: []string{"q", "artist", "album", "year"},
			BookSearch:  []string{"q"},
		},
	}
}
