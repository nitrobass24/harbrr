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

// animebytesCaps is the AnimeBytes capability document, ported byte-for-byte from
// Prowlarr's AnimeBytes.SetCapabilities (AnimeBytes.cs:124-141). The CategoryMapping ID is
// the LITERAL scrape.php filter param key AnimeBytes recognises ("anime[tv_series]",
// "audio", "gamec[game]", "printedtype[manga]", …) — NOT a synthetic id. That id is what
// MapTorznabCapsToTrackers resolves a requested Newznab category to, and what the request
// builder then emits as "<key>=1"; using anything else makes the server-side category
// filter a silent no-op. All music groups map to the single "audio" key (Prowlarr does not
// split music per-format on the request side; the Lossless/MP3/Other refinement is a
// parse-side concern). gamec[game] / gamec[visual_novel] are each mapped to both Console
// and PC/Games, exactly as Prowlarr registers them, so a request for either Newznab cat
// resolves to the same AB key. The search modes mirror Prowlarr's basic q for every type.
func animebytesCaps() loader.Caps {
	return loader.Caps{
		CategoryMappings: []loader.CategoryMapping{
			// Anime video groups -> TV/Anime.
			catDesc("anime[tv_series]", "TV Series", "TV/Anime"),
			catDesc("anime[tv_special]", "TV Special", "TV/Anime"),
			catDesc("anime[ova]", "OVA", "TV/Anime"),
			catDesc("anime[ona]", "ONA", "TV/Anime"),
			catDesc("anime[dvd_special]", "DVD Special", "TV/Anime"),
			catDesc("anime[bd_special]", "BD Special", "TV/Anime"),
			// Movie groups -> Movies.
			catDesc("anime[movie]", "Movie", "Movies"),
			// Music groups -> Audio (single key for ALL music; the Lossless/MP3/Other
			// refinement is applied parse-side).
			catDesc("audio", "Music", "Audio"),
			// Games -> Console AND PC/Games (Prowlarr registers each game key twice).
			catDesc("gamec[game]", "Game", "Console"),
			catDesc("gamec[game]", "Game", "PC/Games"),
			catDesc("gamec[visual_novel]", "Game Visual Novel", "Console"),
			catDesc("gamec[visual_novel]", "Game Visual Novel", "PC/Games"),
			// Printed media -> Books/Comics.
			catDesc("printedtype[manga]", "Manga", "Books"),
			catDesc("printedtype[oneshot]", "Oneshot", "Books"),
			catDesc("printedtype[anthology]", "Anthology", "Books"),
			catDesc("printedtype[manhwa]", "Manhwa", "Books"),
			catDesc("printedtype[light_novel]", "Light Novel", "Books"),
			catDesc("printedtype[artbook]", "Artbook", "Books"),
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

// catDesc builds a categorymapping. id is the LITERAL scrape.php filter param key
// AnimeBytes recognises (e.g. "anime[tv_series]"); it is what MapTorznabCapsToTrackers
// resolves a Newznab category to and what the request builder emits as "<id>=1". name is
// the newznab category; desc is the human-readable label (the AB category description).
func catDesc(id, desc, name string) loader.CategoryMapping {
	return loader.CategoryMapping{ID: loader.Scalar{Value: id, Set: true}, Cat: name, Desc: desc}
}
