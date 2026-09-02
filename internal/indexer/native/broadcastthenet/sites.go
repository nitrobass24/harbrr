package broadcastthenet

import (
	"github.com/autobrr/harbrr/internal/indexer/cardigann/loader"
	"github.com/autobrr/harbrr/internal/indexer/native"
)

// requestDelaySeconds is the between-request pacing for BroadcastTheNet. Prowlarr
// declares RateLimit => TimeSpan.FromSeconds(5) in BroadcastheNet.cs. BTN also enforces
// a 150-requests-per-hour budget (QueryLimit=150, LimitsUnit=Hour); harbrr has no
// per-hour limiter, so only the 5 s delay is expressed here and the registry's paced
// client enforces it.
const requestDelaySeconds = 5.0

// Families returns BroadcastTheNet as a single native family. It carries a Go-built,
// caps-only definition (id/name/type/links/settings/caps) and the New factory; it is
// registered with the registry, not the Cardigann loader. The single apikey setting
// mirrors BroadcastheNetSettings (Prowlarr's PrivacyLevel.ApiKey on the API key).
func Families() []native.Family {
	return []native.Family{
		{Definition: native.Site{
			ID: "broadcastthenet", Name: "BroadcastTheNet", Link: "https://api.broadcasthe.net/",
			Driver: "BroadcastTheNet", DelaySeconds: requestDelaySeconds,
			Settings: []loader.SettingsField{native.FieldAPIKey},
			Caps:     btnCaps(),
		}.Definition(), Factory: New},
	}
}

// btnCaps is the BroadcastTheNet capability document. BTN is a TV-only tracker whose
// newznab category is derived from each torrent's Resolution field (not a tracker
// category id), so the category map keys the Resolution strings — as both the tracker
// id and the description the parser maps through MapTrackerCatDescToNewznab — to
// newznab categories, matching Prowlarr's BroadcastheNet.SetCapabilities:
// SD/Portable Device -> TV/SD, 720p/1080p/1080i -> TV/HD, 2160p -> TV/UHD; the parser
// falls back to TV (5000) for an unmapped resolution. The search modes mirror
// Prowlarr's TvSearchParams: q, season, ep, tvdbid, rid (no imdb).
func btnCaps() loader.Caps {
	return loader.Caps{
		CategoryMappings: native.Cats(
			native.Cat{ID: "SD", Newznab: "TV/SD", Desc: "SD"},
			native.Cat{ID: "Portable Device", Newznab: "TV/SD", Desc: "Portable Device"},
			native.Cat{ID: "720p", Newznab: "TV/HD", Desc: "720p"},
			native.Cat{ID: "1080p", Newznab: "TV/HD", Desc: "1080p"},
			native.Cat{ID: "1080i", Newznab: "TV/HD", Desc: "1080i"},
			native.Cat{ID: "2160p", Newznab: "TV/UHD", Desc: "2160p"},
		),
		Modes: loader.Modes{
			Search:   []string{"q"},
			TVSearch: []string{"q", "season", "ep", "tvdbid", "rid"},
		},
	}
}
