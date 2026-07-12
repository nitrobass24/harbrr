package newznab

import (
	"github.com/autobrr/harbrr/internal/indexer/cardigann/loader"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/mapper"
	"github.com/autobrr/harbrr/internal/indexer/native"
)

// preset is one named Newznab indexer: a distinct id+name, a default base URL the
// instance starts with (the user may still override it), and a seed list of newznab
// category ids the placeholder caps advertise until the live ?t=caps fetch (Leaf 5)
// supersedes them. Every preset shares the generic New factory; only its Definition
// differs (Prowlarr's pattern: one Newznab class, many DefaultDefinitions).
type preset struct {
	id         string
	name       string
	baseURL    string
	categories []int
	// typ overrides the privacy classification ("public" | "semi-private"). Omit for
	// the common case ("private") — most Newznab indexers require an API key.
	typ string
	// settings overrides the default settingFields() when a preset needs custom
	// labels or extra info fields (e.g. an optional-key notice for public indexers).
	settings []loader.SettingsField
}

// presets is the parity table mirroring Prowlarr's Newznab.cs DefaultDefinitions
// (commit bd3bc42): ~18 named usenet indexers, each an IndexerDefinition with
// Implementation = "Newznab", a fixed BaseUrl, and a seed category-id list. Kept as a
// typed Go table (no YAML) because the usenet path is protocol-level, not a Cardigann
// corpus entry. The category seed is a placeholder: a live caps fetch replaces it with
// the remote server's real category tree, so the exact seed only governs the
// pre-caps advertised set. Most presets seed the full standard table (stdCategories);
// AnimeTosho is anime-only and seeds just the anime categories it serves.
var presets = []preset{
	{id: "nzbgeek", name: "NZBgeek", baseURL: "https://api.nzbgeek.info"},
	{id: "dognzb", name: "DOGnzb", baseURL: "https://api.dognzb.cr"},
	{id: "drunkenslug", name: "DrunkenSlug", baseURL: "https://api.drunkenslug.com"},
	{id: "nzbfinder", name: "NZBFinder", baseURL: "https://nzbfinder.ws"},
	{id: "nzbplanet", name: "NZBPlanet", baseURL: "https://api.nzbplanet.net"},
	{id: "nzbcat", name: "NZBCat", baseURL: "https://nzb.cat"},
	{id: "nzbstars", name: "NZBStars", baseURL: "https://nzbstars.com"},
	{id: "abnzb", name: "abNZB", baseURL: "https://abnzb.com"},
	{id: "althub", name: "altHUB", baseURL: "https://api.althub.co.za"},
	{id: "animetosho", name: "AnimeTosho (Usenet)", baseURL: "https://feed.animetosho.org", categories: animeToshoCategories()},
	{id: "gingadaddy", name: "GingaDADDY", baseURL: "https://www.gingadaddy.com"},
	{id: "miatrix", name: "Miatrix", baseURL: "https://www.miatrix.com"},
	{id: "newz69", name: "Newz69", baseURL: "https://newz69.keagaming.com"},
	{id: "ninjacentral", name: "NinjaCentral", baseURL: "https://ninjacentral.co.za"},
	{id: "nzblife", name: "Nzb.life", baseURL: "https://nzb.life"},
	{id: "nzbnoob", name: "NzbNoob", baseURL: "https://www.nzbnoob.com"},
	{id: "nzbndx", name: "NZBNDX", baseURL: "https://www.nzbndx.com"},
	{id: "tabularasa", name: "Tabula Rasa", baseURL: "https://www.tabula-rasa.pw"},
}

// animeToshoCategories is AnimeTosho's seed set: the anime TV/movie newznab categories
// (5070 TV/Anime, 2000 Movies, 5000 TV) it serves before its live caps are fetched.
func animeToshoCategories() []int { return []int{5070, 2000, 5000} }

// Families returns the generic Newznab driver plus every preset, each a native.Family
// sharing the New factory with its own Definition (distinct id/name, a default base URL,
// and a seed category list). All carry Protocol=usenet. This is wired into the registry's
// nativeFamilies(), so the generic indexer and all presets are addable like any other
// native family — Prowlarr's "one Newznab implementation + DefaultDefinitions" shape.
func Families() []native.Family {
	out := make([]native.Family, 0, len(presets)+1)
	out = append(out, Family())
	for _, p := range presets {
		out = append(out, native.Family{Definition: presetDefinition(p), Factory: New})
	}
	return out
}

// presetDefinition builds a preset's caps-only definition. It mirrors the generic
// definition (usenet protocol, the same apikey/apiPath settings, the same request
// pacing) but carries the preset's distinct id/name, a default base-URL link, and a
// preset-specific seed category set. Like the generic definition it is never
// schema-validated; it exists so mapper.Build, the credential store, indexerInfo, and
// the addable-indexer list all work.
func presetDefinition(p preset) *loader.Definition {
	delay := requestDelaySeconds
	typ := p.typ
	if typ == "" {
		typ = "private"
	}
	settings := p.settings
	if settings == nil {
		settings = settingFields()
	}
	return &loader.Definition{
		ID:           p.id,
		Name:         p.name,
		Description:  p.name + " (native Newznab driver)",
		Language:     "en-US",
		Type:         typ,
		Encoding:     "UTF-8",
		Protocol:     loader.ProtocolUsenet,
		Links:        []string{p.baseURL},
		RequestDelay: &delay,
		Settings:     settings,
		Caps:         presetCaps(p.categories),
	}
}

// presetCaps builds the placeholder caps for a preset. With no seed categories it reuses
// the generic full standard table (placeholderCaps); with a seed list it advertises only
// those standard categories, mapped 1:1 by id (the same identity round-trip the generic
// table uses). Search modes always advertise the full common set so a caller may issue
// any search; the remote server's own caps gate which params it honors once Leaf 5 lands.
func presetCaps(categoryIDs []int) loader.Caps {
	if len(categoryIDs) == 0 {
		return placeholderCaps()
	}
	allowIMDB := true
	mappings := make([]loader.CategoryMapping, 0, len(categoryIDs))
	for _, id := range categoryIDs {
		cat, ok := mapper.GetByID(id)
		if !ok {
			continue
		}
		mappings = append(mappings, loader.CategoryMapping{
			ID:   loader.Scalar{Value: itoa(cat.ID), Set: true},
			Cat:  cat.Name,
			Desc: cat.Name,
		})
	}
	return loader.Caps{
		CategoryMappings:  mappings,
		Modes:             commonModes(),
		AllowTVSearchIMDB: &allowIMDB,
	}
}
