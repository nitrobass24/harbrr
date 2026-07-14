package torznab

import (
	"strconv"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/loader"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/mapper"
	"github.com/autobrr/harbrr/internal/indexer/native"
)

// preset is one named torznab-family indexer: a distinct id+name, a default base URL
// the instance starts with, the site's own API path (fixed per preset — unlike the
// newznab sibling's user-editable apiPath setting, no torznab preset shipped so far
// needs it configurable), and the pass-through torznab category ids the preset
// advertises. Every preset shares the generic New factory; only its Definition
// differs — Prowlarr's pattern for its own Torznab.cs DefaultDefinitions (MoreThanTV,
// AnimeTosho, Torrent Network all share one Torznab class).
//
// There is deliberately NO generic user-supplied-URL entry (mirroring newznab's
// Family()/GenericDefinition()): the issue scoped that as "in scope only if it falls
// out naturally", and the two torznab-family sites it names beyond MoreThanTV
// (AnimeTosho, Torrent Network) are explicitly out of scope for this change, so there
// is no second preset yet to prove the generic shape against. Families() below
// therefore returns only the preset table.
type preset struct {
	id         string
	name       string
	baseURL    string
	apiPath    string
	keyInfoURL string // the User Security / API-key page linked from the settings hint
	categories []int  // pass-through torznab category ids (already canonical — no tracker-id remap)
}

// presets is the torznab-family preset table. MoreThanTV is the first and, for now,
// only entry (Prowlarr Torznab.cs:96; Jackett MoreThanTVAPI.cs).
var presets = []preset{
	{
		id:         "morethantv",
		name:       "MoreThanTV",
		baseURL:    "https://www.morethantv.me/",
		apiPath:    "/api/torznab",
		keyInfoURL: "https://www.morethantv.me/user/security",
		// TVSD, TVHD, TVUHD, TVSport, MoviesSD, MoviesHD, MoviesUHD, MoviesBluRay —
		// Jackett's MoreThanTVAPI.SetCapabilities category-mapping list, id-for-id.
		categories: []int{5030, 5040, 5045, 5060, 2030, 2040, 2045, 2050},
	},
}

// presetByID resolves a preset by its definition id, used by New to look up the
// site's fixed apiPath.
func presetByID(id string) (preset, bool) {
	for _, p := range presets {
		if p.id == id {
			return p, true
		}
	}
	return preset{}, false
}

// Families returns every torznab-family preset as a native.Family sharing the New
// factory with its own Go-built, caps-only Definition. This is wired into the
// registry's native catalog (catalog.All()), so each preset is addable like any other
// native family.
func Families() []native.Family {
	out := make([]native.Family, 0, len(presets))
	for _, p := range presets {
		out = append(out, native.Family{Definition: presetDefinition(p), Factory: New})
	}
	return out
}

// presetDefinition builds a preset's caps-only definition: torrent protocol, the
// preset's default base-URL link, the single "apikey" secret setting (plus its
// display-only key-page hint), and the pass-through category caps. It is never
// schema-validated (no login/search/download block) — it exists so mapper.Build, the
// credential store, indexerInfo, and the addable-indexer list all work.
func presetDefinition(p preset) *loader.Definition {
	return &loader.Definition{
		ID:          p.id,
		Name:        p.name,
		Description: p.name + " (native Torznab driver)",
		Language:    "en-US",
		Type:        "private",
		Encoding:    "UTF-8",
		Protocol:    loader.ProtocolTorrent,
		Links:       []string{p.baseURL},
		Settings:    settingFields(p),
		Caps:        presetCaps(p.categories),
	}
}

// settingFields is the preset's single user-entered field: apikey. It carries the
// "apikey" token so the secret store auto-classifies it (encrypted at rest, redacted
// by the API). keyInfo is a display-only info field pointing at the site's User
// Security page — Jackett's MoreThanTVAPI adds the identical dynamic field.
func settingFields(p preset) []loader.SettingsField {
	return []loader.SettingsField{
		{Name: "apikey", Label: "API Key", Type: "text"},
		{
			Name:  "keyInfo",
			Label: "API Key",
			Type:  "info",
			Default: &loader.Scalar{
				Value: `Find or generate an API key on the ` + p.name + ` <a href="` + p.keyInfoURL + `" target="_blank">User Security</a> page, under the API Keys section.`,
				Set:   true,
			},
		},
	}
}

// presetCaps builds the preset's caps: each pass-through category id maps 1:1 to
// itself (Torznab items already report categories in the canonical vocabulary — no
// tracker-id remap needed, so unlike the newznab sibling's presetCaps this
// deliberately does NOT set CategoryMapping.Desc, so mapCategoryMappings does not
// synthesize a redundant custom (1:1 + 100000) entry for an id that is already
// canonical). tv-search and movie-search mirror Jackett's SetCapabilities exactly;
// search (q only) is always available (Jackett's PerformQuery else-branch).
func presetCaps(categoryIDs []int) loader.Caps {
	allowIMDB := true
	mappings := make([]loader.CategoryMapping, 0, len(categoryIDs))
	for _, id := range categoryIDs {
		cat, ok := mapper.GetByID(id)
		if !ok {
			continue
		}
		mappings = append(mappings, loader.CategoryMapping{
			ID:  loader.Scalar{Value: strconv.Itoa(id), Set: true},
			Cat: cat.Name,
		})
	}
	return loader.Caps{
		CategoryMappings: mappings,
		Modes: loader.Modes{
			Search:      []string{"q"},
			TVSearch:    []string{"q", "season", "ep", "imdbid", "tvdbid"},
			MovieSearch: []string{"q", "imdbid"},
		},
		AllowTVSearchIMDB: &allowIMDB,
	}
}
