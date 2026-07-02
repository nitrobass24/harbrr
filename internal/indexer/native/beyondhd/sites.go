package beyondhd

import (
	"github.com/autobrr/harbrr/internal/indexer/cardigann/loader"
	"github.com/autobrr/harbrr/internal/indexer/native"
)

// requestDelaySeconds is the between-request pacing for BeyondHD. Prowlarr's
// BeyondHD.cs declares no explicit RateLimit, so this value is not from the upstream
// source; a conservative 2 s RequestDelay rides on the definition and the registry's
// paced client enforces it (mirroring the HDBits choice).
const requestDelaySeconds = 2.0

// Families returns BeyondHD as a single native family. It carries a Go-built, caps-only
// definition (id/name/type/links/settings/caps) and the New factory; it is registered
// with the registry, not the Cardigann loader.
func Families() []native.Family {
	return []native.Family{
		{Definition: siteDef("beyondhd", "BeyondHD", "https://beyond-hd.me/", beyondhdCaps()), Factory: New},
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
		Description:  name + " (native BeyondHD driver)",
		Language:     "en-US",
		Type:         "private",
		Encoding:     "UTF-8",
		Links:        []string{link},
		RequestDelay: &delay,
		Settings:     credentialSettings(),
		Caps:         caps,
	}
}

// credentialSettings are the two user-entered fields, mirroring Prowlarr's
// BeyondHDSettings. Both are secrets (PrivacyLevel.ApiKey, length 32):
//
//   - api_key rides in the secret-bearing URL path ({base}api/torrents/{api_key}); its
//     name carries the "api_key" token, so the secret store auto-classifies it.
//   - rsskey is sent as a body field on every search and is embedded in the download URL;
//     its name carries the "rsskey" token, so it too is auto-classified as a secret.
func credentialSettings() []loader.SettingsField {
	return []loader.SettingsField{
		{Name: "api_key", Label: "API Key", Type: "text"},
		{Name: "rsskey", Label: "RSS Key", Type: "text"},
	}
}

// beyondhdCaps is the BeyondHD capability document. BHD keys each release to a newznab
// category by its `category` description string ("Movies"/"TV"), so the category map
// keys those descriptions to newznab categories, matching Prowlarr's
// BeyondHD.SetCapabilities: 1 Movies->Movies, 2 TV->TV. The search modes mirror
// Prowlarr's SupportedSearchParameters: movie q+imdbid+tmdbid; tv q+season+ep+imdbid
// (no tvdbid, no music/book).
func beyondhdCaps() loader.Caps {
	allowIMDB := true
	return loader.Caps{
		CategoryMappings: []loader.CategoryMapping{
			catDesc("1", "Movies", "Movies"),
			catDesc("2", "TV", "TV"),
		},
		Modes: loader.Modes{
			Search:      []string{"q"},
			MovieSearch: []string{"q", "imdbid", "tmdbid"},
			TVSearch:    []string{"q", "season", "ep", "imdbid"},
		},
		AllowTVSearchIMDB: &allowIMDB,
	}
}

// catDesc builds a categorymapping with a tracker id (the stringified tracker category
// int), the BeyondHD description string (the value the response `category` field carries,
// which the parser maps through MapTrackerCatDescToNewznab), and the newznab category
// name. A desc additionally synthesises Jackett's custom 1:1 category (see
// mapper.mapCategoryMappings).
func catDesc(id, desc, name string) loader.CategoryMapping {
	return loader.CategoryMapping{ID: loader.Scalar{Value: id, Set: true}, Cat: name, Desc: desc}
}
