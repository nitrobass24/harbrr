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
// registered with the registry, not the Cardigann loader.
func Families() []native.Family {
	return []native.Family{
		{Definition: siteDef("broadcastthenet", "BroadcastTheNet", "https://api.broadcasthe.net/", btnCaps()), Factory: New},
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
		Description:  name + " (native BroadcastTheNet driver)",
		Language:     "en-US",
		Type:         "private",
		Encoding:     "UTF-8",
		Links:        []string{link},
		RequestDelay: &delay,
		Settings:     credentialSettings(),
		Caps:         caps,
	}
}

// credentialSettings is the single user-entered field, mirroring BroadcastheNetSettings.
// apikey is text-typed but its name carries the "apikey" token, so harbrr's secret store
// auto-classifies it as a secret (encrypted at rest, redacted by the API) — matching
// Prowlarr's PrivacyLevel.ApiKey on the API key.
func credentialSettings() []loader.SettingsField {
	return []loader.SettingsField{
		{Name: "apikey", Label: "API Key", Type: "text"},
	}
}

// btnCaps is the BroadcastTheNet capability document. BTN is a TV-only tracker whose
// newznab category is derived from each torrent's Resolution field (not a tracker
// category id), so the category map keys the Resolution strings to newznab categories,
// matching Prowlarr's BroadcastheNet.SetCapabilities: SD/Portable Device -> TV/SD,
// 720p/1080p/1080i -> TV/HD, 2160p -> TV/UHD; the parser falls back to TV (5000) for an
// unmapped resolution. The search modes mirror Prowlarr's TvSearchParams: q, season, ep,
// tvdbid, rid (no imdb).
func btnCaps() loader.Caps {
	return loader.Caps{
		CategoryMappings: []loader.CategoryMapping{
			catDesc("SD", "SD", "TV/SD"),
			catDesc("Portable Device", "Portable Device", "TV/SD"),
			catDesc("720p", "720p", "TV/HD"),
			catDesc("1080p", "1080p", "TV/HD"),
			catDesc("1080i", "1080i", "TV/HD"),
			catDesc("2160p", "2160p", "TV/UHD"),
		},
		Modes: loader.Modes{
			Search:   []string{"q"},
			TVSearch: []string{"q", "season", "ep", "tvdbid", "rid"},
		},
	}
}

// catDesc builds a categorymapping with the BroadcastTheNet Resolution string as both the
// tracker id and the description (the value the response `Resolution` field carries, which
// the parser maps through MapTrackerCatDescToNewznab) and the newznab category name. A
// desc additionally synthesises Jackett's custom 1:1 category (see
// mapper.mapCategoryMappings).
func catDesc(id, desc, name string) loader.CategoryMapping {
	return loader.CategoryMapping{ID: loader.Scalar{Value: id, Set: true}, Cat: name, Desc: desc}
}
