package gazellegames

import (
	"github.com/autobrr/harbrr/internal/indexer/cardigann/loader"
	"github.com/autobrr/harbrr/internal/indexer/native"
)

// requestDelaySeconds is the between-request pacing for GazelleGames. autobrr's ggn
// client rate-limits to one request every 5 seconds (rate.Every(5*time.Second)); that
// steady budget is expressed here as a 5 s RequestDelay that rides on the definition so
// the registry's existing paced client enforces it (no special-casing). Prowlarr does
// not declare an explicit delay; this stays within autobrr's measured ceiling.
const requestDelaySeconds = 5.0

// Families returns GazelleGames (GGn) as a single native family. It carries a Go-built,
// caps-only definition (id/name/type/links/settings/caps) and the shared New factory; it
// is registered with the registry, not the Cardigann loader.
func Families() []native.Family {
	return []native.Family{
		{Definition: siteDef("gazellegames", "GazelleGames", "https://gazellegames.net/"), Factory: New},
	}
}

// siteDef builds the family's caps-only definition. It is never schema-validated (it has
// no login/search/download block); it exists so mapper.Build, the credential store
// (settingFields/IsSecret), indexerInfo, and the addable-indexer list all work for a
// native family with no special case.
func siteDef(id, name, link string) *loader.Definition {
	delay := requestDelaySeconds
	return &loader.Definition{
		ID:           id,
		Name:         name,
		Description:  name + " (native Gazelle-family games driver)",
		Language:     "en-US",
		Type:         "private",
		Encoding:     "UTF-8",
		Links:        []string{link},
		RequestDelay: &delay,
		Settings:     credentialSettings(),
		Caps:         gazellegamesCaps(),
	}
}

// credentialSettings are the user-entered fields. apikey is text-typed but its name
// carries the "apikey" token, so harbrr's secret store auto-classifies it as a secret
// (encrypted at rest, redacted by the API) — matching Prowlarr's PrivacyLevel.ApiKey.
// The download passkey is NOT a user setting: GGn exposes it via request=quick_user, so
// a later leaf fetches it with the apikey and persists it via PersistSetting.
func credentialSettings() []loader.SettingsField {
	return []loader.SettingsField{
		{Name: "apikey", Label: "API Key", Type: "text"},
	}
}

// gazellegamesCaps is the GazelleGames capability document. The category map keys the
// tracker's numeric group categoryId to its newznab category AND the tracker's category
// description, ported from Prowlarr's GazelleGames.SetCapabilities numeric mappings:
// 1->PC/Games("Games"), 2->PC/0day("Applications"), 3->Books/EBook("E-Books"),
// 4->Audio/Other("OST"). GGn additionally maps a long list of platform-name categories
// (Windows, Mac, …) used only for the search-category filter; those are added in the
// leaf that wires search filtering, not here. The search mode is basic text only (q):
// GGn's api.php?request=search takes a searchstr/groupname term, with no structured
// movie/tv/music/book parameters.
func gazellegamesCaps() loader.Caps {
	return loader.Caps{
		CategoryMappings: []loader.CategoryMapping{
			cat("1", "PC/Games", "Games"),
			cat("2", "PC/0day", "Applications"),
			cat("3", "Books/EBook", "E-Books"),
			cat("4", "Audio/Other", "OST"),
		},
		Modes: loader.Modes{
			Search: []string{"q"},
		},
	}
}

// cat builds a categorymapping with a tracker id, the newznab category name, and the
// tracker's category description string (the value the response's textual category
// carries, mapped via MapTrackerCatDescToNewznab).
func cat(id, name, desc string) loader.CategoryMapping {
	return loader.CategoryMapping{ID: loader.Scalar{Value: id, Set: true}, Cat: name, Desc: desc}
}
