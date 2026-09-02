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
//
// The two settings mirror PassThePopcornSettings. BOTH are secrets: apiuser (Prowlarr
// PrivacyLevel.UserName) and apikey (PrivacyLevel.ApiKey) are carried as the ApiUser /
// ApiKey request headers and must never be logged. apikey is auto-classified by the
// "apikey" name token; apiuser is force-typed "password" (always a secret per IsSecret,
// so it stays an inline literal) — its name alone carries no credential token and would
// not trip the classifier.
func Families() []native.Family {
	return []native.Family{
		{Definition: native.Site{
			ID: "passthepopcorn", Name: "PassThePopcorn", Link: "https://passthepopcorn.me/",
			Driver: "PassThePopcorn", DelaySeconds: requestDelaySeconds,
			Settings: []loader.SettingsField{
				{Name: "apiuser", Label: "API User", Type: "password"},
				native.FieldAPIKey,
			},
			Caps: ptpCaps(),
		}.Definition(), Factory: New},
	}
}

// ptpCaps is the PassThePopcorn capability document. PTP is a movie-only tracker whose
// newznab category is derived from each movie-group's CategoryId (1-6) — the value the
// response `CategoryId` field carries, which the parser maps through
// MapTrackerCatToNewznab — all of which map to Movies (2000) per Prowlarr's
// PassThePopcorn.SetCapabilities; the description carries PTP's human label. The search
// modes mirror Prowlarr's MovieSearchParams: q, imdbid (both flow into the single
// searchstr param — there is no separate imdb query param).
func ptpCaps() loader.Caps {
	return loader.Caps{
		CategoryMappings: native.Cats(
			native.Cat{ID: "1", Newznab: "Movies", Desc: "Feature Film"},
			native.Cat{ID: "2", Newznab: "Movies", Desc: "Short Film"},
			native.Cat{ID: "3", Newznab: "Movies", Desc: "Miniseries"},
			native.Cat{ID: "4", Newznab: "Movies", Desc: "Stand-up Comedy"},
			native.Cat{ID: "5", Newznab: "Movies", Desc: "Live Performance"},
			native.Cat{ID: "6", Newznab: "Movies", Desc: "Movie Collection"},
		),
		Modes: loader.Modes{
			Search:      []string{"q"},
			MovieSearch: []string{"q", "imdbid"},
		},
	}
}
