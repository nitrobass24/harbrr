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
//
// The settings mirror AnimeBytesSettings. username is the account identifier (stored
// as-is). passkey is text-typed but its name carries the "passkey" token, so harbrr's
// secret store auto-classifies it as a secret (encrypted at rest, redacted by the API)
// — matching Prowlarr's PrivacyLevel.Password. Both ride in the search/download URL
// query, so that URL is secret-bearing and must be redacted everywhere.
func Families() []native.Family {
	return []native.Family{
		{Definition: native.Site{
			ID: "animebytes", Name: "AnimeBytes", Link: "https://animebytes.tv/",
			Driver: "AnimeBytes", DelaySeconds: requestDelaySeconds,
			Settings: []loader.SettingsField{native.FieldUsername, native.FieldPasskey},
			Caps:     animebytesCaps(),
		}.Definition(), Factory: New},
	}
}

// animebytesCaps is the AnimeBytes capability document, ported byte-for-byte from
// Prowlarr's AnimeBytes.SetCapabilities (AnimeBytes.cs:124-141). The Cat row's ID is
// the LITERAL scrape.php filter param key AnimeBytes recognises ("anime[tv_series]",
// "audio", "gamec[game]", "printedtype[manga]", …) — NOT a synthetic id. That id is what
// MapTorznabCapsToTrackers resolves a requested Newznab category to, and what the request
// builder then emits as "<key>=1"; using anything else makes the server-side category
// filter a silent no-op. All music groups map to the single "audio" key (Prowlarr does not
// split music per-format on the request side; the Lossless/MP3/Other refinement is a
// parse-side concern). gamec[game] / gamec[visual_novel] are each mapped to both Console
// and PC/Games, exactly as Prowlarr registers them, so a request for either Newznab cat
// resolves to the same AB key. The search modes mirror Prowlarr's (basic q, plus
// MusicSearch q/artist/album/year — a music query is routed to AnimeBytes' music corpus
// via search.Query.Mode; see the Modes block).
func animebytesCaps() loader.Caps {
	return loader.Caps{
		CategoryMappings: native.Cats(
			// Anime video groups -> TV/Anime.
			native.Cat{ID: "anime[tv_series]", Newznab: "TV/Anime", Desc: "TV Series"},
			native.Cat{ID: "anime[tv_special]", Newznab: "TV/Anime", Desc: "TV Special"},
			native.Cat{ID: "anime[ova]", Newznab: "TV/Anime", Desc: "OVA"},
			native.Cat{ID: "anime[ona]", Newznab: "TV/Anime", Desc: "ONA"},
			native.Cat{ID: "anime[dvd_special]", Newznab: "TV/Anime", Desc: "DVD Special"},
			native.Cat{ID: "anime[bd_special]", Newznab: "TV/Anime", Desc: "BD Special"},
			// Movie groups -> Movies.
			native.Cat{ID: "anime[movie]", Newznab: "Movies", Desc: "Movie"},
			// Music groups -> Audio (single key for ALL music; the Lossless/MP3/Other
			// refinement is applied parse-side).
			native.Cat{ID: "audio", Newznab: "Audio", Desc: "Music"},
			// Games -> Console AND PC/Games (Prowlarr registers each game key twice).
			native.Cat{ID: "gamec[game]", Newznab: "Console", Desc: "Game"},
			native.Cat{ID: "gamec[game]", Newznab: "PC/Games", Desc: "Game"},
			native.Cat{ID: "gamec[visual_novel]", Newznab: "Console", Desc: "Game Visual Novel"},
			native.Cat{ID: "gamec[visual_novel]", Newznab: "PC/Games", Desc: "Game Visual Novel"},
			// Printed media -> Books/Comics.
			native.Cat{ID: "printedtype[manga]", Newznab: "Books", Desc: "Manga"},
			native.Cat{ID: "printedtype[oneshot]", Newznab: "Books", Desc: "Oneshot"},
			native.Cat{ID: "printedtype[anthology]", Newznab: "Books", Desc: "Anthology"},
			native.Cat{ID: "printedtype[manhwa]", Newznab: "Books", Desc: "Manhwa"},
			native.Cat{ID: "printedtype[light_novel]", Newznab: "Books", Desc: "Light Novel"},
			native.Cat{ID: "printedtype[artbook]", Newznab: "Books", Desc: "Artbook"},
		),
		// MusicSearch is advertised: search.Query.Mode now carries the Torznab t= mode
		// (set by the feed/JSON handler), so searchTypeFor routes a music-search query —
		// including a keyword-only one with no artist/album — to AnimeBytes' music corpus.
		Modes: loader.Modes{
			Search:      []string{"q"},
			TVSearch:    []string{"q", "season", "ep"},
			MovieSearch: []string{"q"},
			MusicSearch: []string{"q", "artist", "album", "year"},
			BookSearch:  []string{"q"},
		},
	}
}
