package gazelle

import (
	"github.com/autobrr/harbrr/internal/indexer/cardigann/loader"
	"github.com/autobrr/harbrr/internal/indexer/native"
)

// Per-site between-request pacing. autobrr's token buckets are the burst ceilings
// (RED 10 req/10s, OPS 5 req/10s); the steady per-site delay derived from those is
// RED ~1s and OPS ~2s. It rides on the definition's RequestDelay so the registry's
// existing paced client enforces it (no special-casing). Prowlarr itself uses a flat
// 3s for both — these are more permissive but stay within autobrr's measured limits.
const (
	redactedDelaySeconds = 1.0
	orpheusDelaySeconds  = 2.0
)

// Families returns the two Gazelle-family sites (Redacted, Orpheus) as native
// families. Each carries a Go-built, caps-only definition (id/name/type/links/
// settings/caps) and the shared New factory; the per-site behaviour (RED's bare
// Authorization header vs OPS's "token " prefix and never-usetoken=0 quirk) is keyed
// off the definition id inside the driver. They are registered with the registry, not
// the Cardigann loader.
func Families() []native.Family {
	return []native.Family{
		{Definition: siteDef("redacted", "Redacted", "https://redacted.sh/", redactedDelaySeconds), Factory: New},
		{Definition: siteDef("orpheus", "Orpheus", "https://orpheus.network/", orpheusDelaySeconds), Factory: New},
	}
}

// siteDef builds one family's caps-only definition. It is never schema-validated (it
// has no login/search/download block); it exists so mapper.Build, the credential store
// (settingFields/IsSecret), indexerInfo, and the addable-indexer list all work for a
// native family with no special case.
func siteDef(id, name, link string, delaySeconds float64) *loader.Definition {
	delay := delaySeconds
	return &loader.Definition{
		ID:           id,
		Name:         name,
		Description:  name + " (native Gazelle-family driver)",
		Language:     "en-US",
		Type:         "private",
		Encoding:     "UTF-8",
		Links:        []string{link},
		RequestDelay: &delay,
		Settings:     credentialSettings(),
		Caps:         gazelleCaps(),
	}
}

// credentialSettings are the user-entered fields. apikey is text-typed but its name
// carries the "apikey" token, so harbrr's secret store auto-classifies it as a secret
// (encrypted at rest, redacted by the API) — matching Prowlarr's PrivacyLevel.ApiKey.
// use_freeleech_token is a checkbox toggle that adds &usetoken=1 to the download URL.
func credentialSettings() []loader.SettingsField {
	return []loader.SettingsField{
		{Name: "apikey", Label: "API Key", Type: "text"},
		{Name: "use_freeleech_token", Label: "Use freeleech token", Type: "checkbox"},
	}
}

// gazelleCaps is the Gazelle (RED/OPS) capability document, identical for both sites
// per Prowlarr's RED.cs / Orpheus.cs SetCapabilities. The category map keys the
// tracker's numeric category id to its newznab category AND the tracker's category
// DESCRIPTION (so a browse result's textual Category — "Music", "Audiobooks", … —
// maps via MapTrackerCatDescToNewznab): 1->Audio("Music"), 2->PC("Applications"),
// 3->Books/EBook("E-Books"), 4->Audio/Audiobook("Audiobooks"), 5->Other("E-Learning
// Videos"), 6->Other("Comedy"), 7->Books/Comics("Comics"). The search modes mirror
// RED/OPS MusicSearchParams (q/artist/album/year — no label) plus basic q and book q.
func gazelleCaps() loader.Caps {
	return loader.Caps{
		CategoryMappings: []loader.CategoryMapping{
			cat("1", "Audio", "Music"),
			cat("2", "PC", "Applications"),
			cat("3", "Books/EBook", "E-Books"),
			cat("4", "Audio/Audiobook", "Audiobooks"),
			cat("5", "Other", "E-Learning Videos"),
			cat("6", "Other", "Comedy"),
			cat("7", "Books/Comics", "Comics"),
		},
		Modes: loader.Modes{
			Search:      []string{"q"},
			MusicSearch: []string{"q", "artist", "album", "year"},
			BookSearch:  []string{"q"},
		},
	}
}

func cat(id, name, desc string) loader.CategoryMapping {
	return loader.CategoryMapping{ID: loader.Scalar{Value: id, Set: true}, Cat: name, Desc: desc}
}
