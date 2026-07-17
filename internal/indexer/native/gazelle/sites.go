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
	// downloadViaTorrents routes the download link through torrents.php instead of
	// ajax.php (AlphaRatio).
	downloadViaTorrents bool
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

// siteConfigs is the Gazelle family's data table: one entry per site, keyed by
// definition id. Adding a site (the planned #28-#31) is a table entry here — never an
// edit to auth.go/parse.go/search.go/grab.go.
var siteConfigs = map[string]siteConfig{
	"redacted": {
		strategy:             apiKeyAuth{},
		classify:             native.ClassifyAuth403,
		countsFreeloadAsFree: true,
	},
	"orpheus": {
		strategy: apiKeyAuth{prefix: "token "},
		classify: native.ClassifyAuth403,
	},
	"alpharatio": {
		strategy:             formLoginAuth{},
		sessionCookieSetting: alphaRatioCookieSetting,
		classify:             classifyFormLogin,
		disableRedirects:     true,
		downloadViaTorrents:  true,
		pageSize:             50,
		minimumRatio:         1,
		minimumSeedTime:      259200,
		buildQuery:           alphaRatioBuildQuery,
		parseProfile:         alphaRatioParseProfile,
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
	if imdbID := fullIMDBID(q.IMDBID); imdbID != "" {
		params.Set("taglist", imdbID)
	}
	if truthy(d.Cfg["freeleech_only"]) {
		params.Set("freetorrent", "1")
	}
	if truthy(d.Cfg["exclude_scene"]) {
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
)

// Families returns the Gazelle-family sites as native families. Each carries a
// Go-built, caps-only definition and the shared New factory; per-site auth and parsing
// behavior is keyed by definition id inside the driver.
func Families() []native.Family {
	return []native.Family{
		{Definition: siteDef("redacted", "Redacted", "https://redacted.sh/", redactedDelaySeconds), Factory: New},
		{Definition: siteDef("orpheus", "Orpheus", "https://orpheus.network/", orpheusDelaySeconds), Factory: New},
		{Definition: alphaRatioDef(), Factory: New},
	}
}

func alphaRatioDef() *loader.Definition {
	delay := alphaRatioDelaySeconds
	return &loader.Definition{
		ID:           "alpharatio",
		Name:         "AlphaRatio",
		Description:  "AlphaRatio (native Gazelle-family driver)",
		Language:     "en-US",
		Type:         "private",
		Encoding:     "UTF-8",
		Links:        []string{"https://alpharatio.cc/"},
		RequestDelay: &delay,
		Settings:     alphaRatioSettings(),
		Caps:         alphaRatioCaps(),
	}
}

func alphaRatioSettings() []loader.SettingsField {
	return []loader.SettingsField{
		{Name: "username", Label: "Username", Type: "text", Required: true},
		{Name: "password", Label: "Password", Type: "password", Required: true},
		{Name: "use_freeleech_token", Label: "Use freeleech token", Type: "checkbox"},
		{Name: "freeleech_only", Label: "Only freeleech", Type: "checkbox"},
		{Name: "exclude_scene", Label: "Exclude scene releases", Type: "checkbox"},
	}
}

func alphaRatioCaps() loader.Caps {
	return loader.Caps{
		CategoryMappings: []loader.CategoryMapping{
			cat("1", "TV/SD", "TvSD"),
			cat("2", "TV/HD", "TvHD"),
			cat("3", "TV/UHD", "TvUHD"),
			cat("4", "TV/SD", "TvDVDRip"),
			cat("5", "TV/SD", "TvPackSD"),
			cat("6", "TV/HD", "TvPackHD"),
			cat("7", "TV/UHD", "TvPackUHD"),
			cat("8", "Movies/SD", "MovieSD"),
			cat("9", "Movies/HD", "MovieHD"),
			cat("10", "Movies/UHD", "MovieUHD"),
			cat("11", "Movies/SD", "MoviePackSD"),
			cat("12", "Movies/HD", "MoviePackHD"),
			cat("13", "Movies/UHD", "MoviePackUHD"),
			cat("14", "XXX", "MovieXXX"),
			cat("15", "Movies/BluRay", "Bluray"),
			cat("16", "TV/Anime", "AnimeSD"),
			cat("17", "TV/Anime", "AnimeHD"),
			cat("18", "PC/Games", "GamesPC"),
			cat("19", "Console/XBox", "GamesxBox"),
			cat("20", "Console/PS4", "GamesPS"),
			cat("21", "Console/Wii", "GamesNin"),
			cat("22", "PC/0day", "AppsWindows"),
			cat("23", "PC/Mac", "AppsMAC"),
			cat("24", "PC/0day", "AppsLinux"),
			cat("25", "PC/Mobile-Other", "AppsMobile"),
			cat("26", "XXX", "0dayXXX"),
			cat("27", "Books", "eBook"),
			cat("28", "Audio/Audiobook", "AudioBook"),
			cat("29", "Audio/Other", "Music"),
			cat("30", "Other", "Misc"),
		},
		Modes: loader.Modes{
			Search:      []string{"q"},
			MovieSearch: []string{"q", "imdbid"},
			TVSearch:    []string{"q", "season", "ep"},
		},
	}
}

// siteDef builds one family's caps-only definition. It is never schema-validated (it
// has no login/search/download block); it exists so mapper.Build, the credential store
// (settingFields/IsSecret), indexerInfo, and the addable-indexer list all work for a
// native family with no special case.
func siteDef(id, name, link string, delaySeconds float64) *loader.Definition {
	delay := delaySeconds
	return &loader.Definition{
		ID:           id,
		Name:         name,
		Description:  name + " (native Gazelle-family driver)",
		Language:     "en-US",
		Type:         "private",
		Encoding:     "UTF-8",
		Links:        []string{link},
		RequestDelay: &delay,
		Settings:     credentialSettings(),
		Caps:         gazelleCaps(),
	}
}

// credentialSettings are the user-entered fields. apikey is text-typed but its name
// carries the "apikey" token, so harbrr's secret store auto-classifies it as a secret
// (encrypted at rest, redacted by the API) — matching Prowlarr's PrivacyLevel.ApiKey.
// use_freeleech_token is a checkbox toggle that adds &usetoken=1 to the download URL.
func credentialSettings() []loader.SettingsField {
	return []loader.SettingsField{
		{Name: "apikey", Label: "API Key", Type: "text"},
		{Name: "use_freeleech_token", Label: "Use freeleech token", Type: "checkbox"},
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
		CategoryMappings: []loader.CategoryMapping{
			cat("1", "Audio", "Music"),
			cat("2", "PC", "Applications"),
			cat("3", "Books/EBook", "E-Books"),
			cat("4", "Audio/Audiobook", "Audiobooks"),
			cat("5", "Other", "E-Learning Videos"),
			cat("6", "Other", "Comedy"),
			cat("7", "Books/Comics", "Comics"),
		},
		Modes: loader.Modes{
			Search:      []string{"q"},
			MusicSearch: []string{"q", "artist", "album", "year"},
			BookSearch:  []string{"q"},
		},
	}
}

func cat(id, name, desc string) loader.CategoryMapping {
	return loader.CategoryMapping{ID: loader.Scalar{Value: id, Set: true}, Cat: name, Desc: desc}
}
