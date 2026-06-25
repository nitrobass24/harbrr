package animebytes

import (
	"github.com/autobrr/harbrr/internal/indexer/cardigann/loader"
	"github.com/autobrr/harbrr/internal/indexer/native"
)

// requestDelaySeconds is the between-request pacing for AnimeBytes. Prowlarr's
// AnimeBytes indexer declares a 4 s rate limit between requests; harbrr expresses that
// as a 4 s RequestDelay on the definition so the registry's existing paced client
// enforces it (no special-casing).
const requestDelaySeconds = 4.0

// Families returns AnimeBytes as a single native family. It carries a Go-built,
// caps-only definition (id/name/type/links/settings/caps) and the New factory; it is
// registered with the registry, not the Cardigann loader.
func Families() []native.Family {
	return []native.Family{
		{Definition: siteDef("animebytes", "AnimeBytes", "https://animebytes.tv/", animebytesCaps()), Factory: New},
	}
}

// siteDef builds the family's caps-only definition. It is never schema-validated (it
// has no login/search/download block); it exists so mapper.Build, the credential store
// (settingFields/IsSecret), indexerInfo, and the addable-indexer list all work for a
// native family with no special case.
func siteDef(id, name, link string, caps loader.Caps) *loader.Definition {
	delay := requestDelaySeconds
	return &loader.Definition{
		ID:           id,
		Name:         name,
		Description:  name + " (native AnimeBytes driver)",
		Language:     "en-US",
		Type:         "private",
		Encoding:     "UTF-8",
		Links:        []string{link},
		RequestDelay: &delay,
		Settings:     credentialSettings(),
		Caps:         caps,
	}
}

// credentialSettings are the user-entered fields, mirroring AnimeBytesSettings.
// username is the account identifier (stored as-is). passkey is text-typed but its name
// carries the "passkey" token, so harbrr's secret store auto-classifies it as a secret
// (encrypted at rest, redacted by the API) — matching Prowlarr's PrivacyLevel.Password.
// Both ride in the search/download URL query, so that URL is secret-bearing and must be
// redacted everywhere.
func credentialSettings() []loader.SettingsField {
	return []loader.SettingsField{
		{Name: "username", Label: "Username", Type: "text"},
		{Name: "passkey", Label: "Passkey", Type: "text"},
	}
}

// animebytesCaps is the AnimeBytes capability document, ported from Prowlarr's
// AnimeBytes.SetCapabilities / AnimeBytesParser category logic. The map is keyed by the
// tracker category DESCRIPTION (the response GroupName / CategoryName string the parser
// maps via MapTrackerCatDescToNewznab): the anime "TV Series"/"OVA"/"ONA" groups ->
// TV/Anime; "Movie"/"Live Action Movie" -> Movies; manga/novel/artbook groups ->
// Books; games -> Console; visual novels -> PC/Games; music groups -> Audio (with the
// Property-driven Lossless/MP3/Other refinement happening parse-side). The search modes
// mirror Prowlarr's basic q for every type (the AB scrape API takes searchstr).
func animebytesCaps() loader.Caps {
	return loader.Caps{
		CategoryMappings: []loader.CategoryMapping{
			// Anime video groups -> TV/Anime.
			catDesc("anime_tv_series", "TV Series", "TV/Anime"),
			catDesc("anime_ova", "OVA", "TV/Anime"),
			catDesc("anime_ona", "ONA", "TV/Anime"),
			// Movie groups -> Movies.
			catDesc("anime_movie", "Movie", "Movies"),
			catDesc("anime_live_action_movie", "Live Action Movie", "Movies"),
			// Printed media -> Books.
			catDesc("printed_manga", "Manga", "Books"),
			catDesc("printed_oneshot", "Oneshot", "Books"),
			catDesc("printed_anthology", "Anthology", "Books"),
			catDesc("printed_manhwa", "Manhwa", "Books"),
			catDesc("printed_manhua", "Manhua", "Books"),
			catDesc("printed_light_novel", "Light Novel", "Books"),
			catDesc("printed_novel", "Novel", "Books"),
			catDesc("printed_artbook", "Artbook", "Books"),
			// Games -> Console; visual novels -> PC/Games.
			catDesc("game", "Game", "Console"),
			catDesc("game_visual_novel", "Visual Novel", "PC/Games"),
			// Music groups -> Audio (Property-driven Lossless/MP3/Other refinement is
			// applied parse-side; the catch-all maps to Audio).
			catDesc("music_album", "Album", "Audio"),
			catDesc("music_single", "Single", "Audio"),
			catDesc("music_soundtrack", "Soundtrack", "Audio"),
			catDesc("music_ep", "EP", "Audio"),
			catDesc("music_live_album", "Live Album", "Audio"),
			catDesc("music_compilation", "Compilation", "Audio"),
			catDesc("music_remix", "Remix", "Audio"),
			catDesc("music_pv", "PV", "Audio"),
			catDesc("music_live", "Live", "Audio"),
		},
		Modes: loader.Modes{
			Search:      []string{"q"},
			TVSearch:    []string{"q", "season", "ep"},
			MovieSearch: []string{"q"},
			MusicSearch: []string{"q"},
			BookSearch:  []string{"q"},
		},
	}
}

// catDesc builds a categorymapping with a synthetic tracker id, the newznab category
// name, and the AnimeBytes category DESCRIPTION (the GroupName/CategoryName the response
// carries and the parser maps through MapTrackerCatDescToNewznab). The id is synthetic
// because the AB scrape API has no numeric category id; it exists only to satisfy the
// mapper's id-keyed structure.
func catDesc(id, desc, name string) loader.CategoryMapping {
	return loader.CategoryMapping{ID: loader.Scalar{Value: id, Set: true}, Cat: name, Desc: desc}
}
