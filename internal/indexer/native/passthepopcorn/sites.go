package passthepopcorn

import (
	"github.com/autobrr/harbrr/internal/indexer/cardigann/loader"
	"github.com/autobrr/harbrr/internal/indexer/native"
)

// requestDelaySeconds is the between-request pacing for PassThePopcorn. Prowlarr
// declares RateLimit => TimeSpan.FromSeconds(4) in PassThePopcorn.cs. PTP also enforces
// a 150-requests-per-hour budget (QueryLimit=150, LimitsUnit=Hour); harbrr has no
// per-hour limiter, so only the 4 s delay is expressed here and the registry's paced
// client enforces it.
const requestDelaySeconds = 4.0

// Families returns PassThePopcorn as a single native family. It carries a Go-built,
// caps-only definition (id/name/type/links/settings/caps) and the New factory; it is
// registered with the registry, not the Cardigann loader.
func Families() []native.Family {
	return []native.Family{
		{Definition: siteDef("passthepopcorn", "PassThePopcorn", "https://passthepopcorn.me/", ptpCaps()), Factory: New},
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
		Description:  name + " (native PassThePopcorn driver)",
		Language:     "en-US",
		Type:         "private",
		Encoding:     "UTF-8",
		Links:        []string{link},
		RequestDelay: &delay,
		Settings:     credentialSettings(),
		Caps:         caps,
	}
}

// credentialSettings are the two user-entered fields, mirroring PassThePopcornSettings.
// BOTH are secrets: apiuser (Prowlarr PrivacyLevel.UserName) and apikey (PrivacyLevel.
// ApiKey) are carried as the ApiUser / ApiKey request headers and must never be logged.
// apikey is auto-classified by the "apikey" name token; apiuser is force-typed "password"
// (always a secret per IsSecret) so the secret store encrypts/redacts it too — its name
// alone carries no credential token and would not trip the classifier.
func credentialSettings() []loader.SettingsField {
	return []loader.SettingsField{
		{Name: "apiuser", Label: "API User", Type: "password"},
		{Name: "apikey", Label: "API Key", Type: "text"},
	}
}

// ptpCaps is the PassThePopcorn capability document. PTP is a movie-only tracker whose
// newznab category is derived from each movie-group's CategoryId (1-6), all of which map
// to Movies (2000) per Prowlarr's PassThePopcorn.SetCapabilities (Feature Film, Short
// Film, Miniseries, Stand-up Comedy, Live Performance, Movie Collection). The search
// modes mirror Prowlarr's MovieSearchParams: q, imdbid (both flow into the single
// searchstr param — there is no separate imdb query param).
func ptpCaps() loader.Caps {
	return loader.Caps{
		CategoryMappings: []loader.CategoryMapping{
			catDesc("1", "Feature Film", "Movies"),
			catDesc("2", "Short Film", "Movies"),
			catDesc("3", "Miniseries", "Movies"),
			catDesc("4", "Stand-up Comedy", "Movies"),
			catDesc("5", "Live Performance", "Movies"),
			catDesc("6", "Movie Collection", "Movies"),
		},
		Modes: loader.Modes{
			Search:      []string{"q"},
			MovieSearch: []string{"q", "imdbid"},
		},
	}
}

// catDesc builds a categorymapping with the PassThePopcorn CategoryId as the tracker id
// (the value the response `CategoryId` field carries, which the parser maps through
// MapTrackerCatToNewznab) and the newznab category name; the description carries PTP's
// human label. A desc additionally synthesises Jackett's custom 1:1 category (see
// mapper.mapCategoryMappings).
func catDesc(id, desc, name string) loader.CategoryMapping {
	return loader.CategoryMapping{ID: loader.Scalar{Value: id, Set: true}, Cat: name, Desc: desc}
}
