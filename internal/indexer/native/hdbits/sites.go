package hdbits

import (
	"github.com/autobrr/harbrr/internal/indexer/cardigann/loader"
	"github.com/autobrr/harbrr/internal/indexer/native"
)

// requestDelaySeconds is the between-request pacing for HDBits. Prowlarr declares no
// explicit RateLimit on the HDBits indexer; the API instead enforces a per-query budget
// (HTTP 403 once the query/rate-limit is reached). harbrr has no per-hour limiter, so a
// conservative 2 s RequestDelay rides on the definition and the registry's paced client
// enforces it (no special-casing).
const requestDelaySeconds = 2.0

// Families returns HDBits as a single native family. It carries a Go-built, caps-only
// definition (id/name/type/links/settings/caps) and the New factory; it is registered
// with the registry, not the Cardigann loader.
//
// The settings mirror Prowlarr's HDBitsSettings. Both username and passkey are secrets:
// Prowlarr marks Username PrivacyLevel.UserName and ApiKey (serialized as the "passkey"
// body/URL field) PrivacyLevel.ApiKey, and both ride in the secret-bearing POST body.
// passkey's name carries the "passkey" token, so the secret store auto-classifies it.
// username has no secret token in its name, so it is typed "password" (an inline
// literal, not native.FieldUsername) to force the same classification — encrypted at
// rest, redacted by the API.
func Families() []native.Family {
	return []native.Family{
		{Definition: native.Site{
			ID: "hdbits", Name: "HDBits", Link: "https://hdbits.org/",
			Driver: "HDBits", DelaySeconds: requestDelaySeconds,
			Settings: []loader.SettingsField{
				{Name: "username", Label: "Username", Type: "password"},
				native.FieldPasskey,
			},
			Caps: hdbitsCaps(),
		}.Definition(), Factory: New},
	}
}

// hdbitsCaps is the HDBits capability document. HDBits keys each release to a newznab
// category by its integer type_category field (1..8), so the category map keys the
// stringified tracker id to a newznab category, matching Prowlarr's
// HDBits.SetCapabilities: 1 Movie->Movies, 2 TV->TV, 3 Documentary->TV/Documentary,
// 4 Music->Audio, 5 Sport->TV/Sport, 6 Audio Track->Audio, 7 XXX->XXX, 8 Misc/Demo->Other.
// Categories 4 and 6 both collapse to Audio (3000); both tracker descriptions are kept so
// the torznab caps round-trip. The search modes mirror Prowlarr's SupportedSearchParameters:
// basic q; movie q+imdbid; tv q+imdbid+season+ep (tvdbid is the wire id but the request
// generator resolves it from the season/ep query, so only the standard params are advertised).
func hdbitsCaps() loader.Caps {
	allowIMDB := true
	return loader.Caps{
		CategoryMappings: native.Cats(
			native.Cat{ID: "1", Newznab: "Movies", Desc: "Movie"},
			native.Cat{ID: "2", Newznab: "TV", Desc: "TV"},
			native.Cat{ID: "3", Newznab: "TV/Documentary", Desc: "Documentary"},
			native.Cat{ID: "4", Newznab: "Audio", Desc: "Music"},
			native.Cat{ID: "5", Newznab: "TV/Sport", Desc: "Sport"},
			native.Cat{ID: "6", Newznab: "Audio", Desc: "Audio Track"},
			native.Cat{ID: "7", Newznab: "XXX", Desc: "XXX"},
			native.Cat{ID: "8", Newznab: "Other", Desc: "Misc/Demo"},
		),
		Modes: loader.Modes{
			Search:      []string{"q"},
			MovieSearch: []string{"q", "imdbid"},
			TVSearch:    []string{"q", "season", "ep", "imdbid"},
		},
		AllowTVSearchIMDB: &allowIMDB,
	}
}
