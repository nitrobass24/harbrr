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
func Families() []native.Family {
	return []native.Family{
		{Definition: siteDef("hdbits", "HDBits", "https://hdbits.org/", hdbitsCaps()), Factory: New},
	}
}

// siteDef builds the family's caps-only definition. It is never schema-validated (it has
// no login/search/download block); it exists so mapper.Build, the credential store
// (settingFields/IsSecret), indexerInfo, and the addable-indexer list all work for a
// native family with no special case.
func siteDef(id, name, link string, caps loader.Caps) *loader.Definition {
	delay := requestDelaySeconds
	return &loader.Definition{
		ID:           id,
		Name:         name,
		Description:  name + " (native HDBits driver)",
		Language:     "en-US",
		Type:         "private",
		Encoding:     "UTF-8",
		Links:        []string{link},
		RequestDelay: &delay,
		Settings:     credentialSettings(),
		Caps:         caps,
	}
}

// credentialSettings are the user-entered fields, mirroring Prowlarr's HDBitsSettings.
// Both username and passkey are secrets: Prowlarr marks Username PrivacyLevel.UserName
// and ApiKey (serialized as the "passkey" body/URL field) PrivacyLevel.ApiKey, and both
// ride in the secret-bearing POST body. passkey is text-typed but its name carries the
// "passkey" token, so the secret store auto-classifies it. username has no secret token
// in its name, so it is typed "password" to force the same classification — encrypted at
// rest, redacted by the API.
func credentialSettings() []loader.SettingsField {
	return []loader.SettingsField{
		{Name: "username", Label: "Username", Type: "password"},
		{Name: "passkey", Label: "Passkey", Type: "text"},
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
		CategoryMappings: []loader.CategoryMapping{
			catDesc("1", "Movie", "Movies"),
			catDesc("2", "TV", "TV"),
			catDesc("3", "Documentary", "TV/Documentary"),
			catDesc("4", "Music", "Audio"),
			catDesc("5", "Sport", "TV/Sport"),
			catDesc("6", "Audio Track", "Audio"),
			catDesc("7", "XXX", "XXX"),
			catDesc("8", "Misc/Demo", "Other"),
		},
		Modes: loader.Modes{
			Search:      []string{"q"},
			MovieSearch: []string{"q", "imdbid"},
			TVSearch:    []string{"q", "season", "ep", "imdbid"},
		},
		AllowTVSearchIMDB: &allowIMDB,
	}
}

// catDesc builds a categorymapping with a tracker id (the stringified type_category int),
// the newznab category name, and the HDBits description string. A desc additionally
// synthesises Jackett's custom 1:1 category (see mapper.mapCategoryMappings).
func catDesc(id, desc, name string) loader.CategoryMapping {
	return loader.CategoryMapping{ID: loader.Scalar{Value: id, Set: true}, Cat: name, Desc: desc}
}
