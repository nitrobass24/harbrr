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
//
// The two settings mirror Prowlarr's BeyondHDSettings and are both secrets
// (PrivacyLevel.ApiKey, length 32) — inline literals, since neither name matches a kit
// field:
//
//   - api_key rides in the secret-bearing URL path ({base}api/torrents/{api_key}); its
//     name carries the "api_key" token, so the secret store auto-classifies it.
//   - rsskey is sent as a body field on every search and is embedded in the download URL;
//     its name carries the "rsskey" token, so it too is auto-classified as a secret.
func Families() []native.Family {
	return []native.Family{
		{Definition: native.Site{
			ID: "beyondhd", Name: "BeyondHD", Link: "https://beyond-hd.me/",
			Driver: "BeyondHD", DelaySeconds: requestDelaySeconds,
			Settings: []loader.SettingsField{
				{Name: "api_key", Label: "API Key", Type: "text"},
				{Name: "rsskey", Label: "RSS Key", Type: "text"},
			},
			Caps: beyondhdCaps(),
		}.Definition(), Factory: New},
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
		CategoryMappings: native.Cats(
			native.Cat{ID: "1", Newznab: "Movies", Desc: "Movies"},
			native.Cat{ID: "2", Newznab: "TV", Desc: "TV"},
		),
		Modes: loader.Modes{
			Search:      []string{"q"},
			MovieSearch: []string{"q", "imdbid", "tmdbid"},
			TVSearch:    []string{"q", "season", "ep", "imdbid"},
		},
		AllowTVSearchIMDB: &allowIMDB,
	}
}
