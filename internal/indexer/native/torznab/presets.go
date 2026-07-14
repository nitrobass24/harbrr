package torznab

import (
	"strconv"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/loader"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/mapper"
	"github.com/autobrr/harbrr/internal/indexer/native"
)

// keyPolicy is a preset's apikey rule. The 32-char rule is Jackett's
// MoreThanTV-SPECIFIC add-time validation (MoreThanTVAPI.ApplyConfiguration), not a
// family rule: Prowlarr's TorznabSettingsValidator validates nothing for these sites
// (its ApiKeyAllowList is empty), and AnimeTosho is a public feed with no key at all.
type keyPolicy int

const (
	// keyOptional accepts any key, including none, unvalidated — the generic entry's
	// policy (an unknown Torznab server may or may not require one).
	keyOptional keyPolicy = iota
	// keyNone is a public feed: no apikey setting exists, and any stray configured
	// value is dropped so it never rides a request to a keyless server.
	keyNone
	// keyRequired is a private tracker whose key length is not documented: non-empty,
	// no length rule (Prowlarr's posture).
	keyRequired
	// keyRequired32 is MoreThanTV's Jackett rule: non-empty and exactly 32 chars.
	keyRequired32
)

// preset is one named torznab-family indexer: a distinct id+name, a default base URL
// the instance starts with, the site's own API path (fixed per preset — unlike the
// generic entry's user-editable apiPath setting, a preset's path is a site fact), its
// apikey policy, and the pass-through torznab category ids it advertises (empty seeds
// the full standard parent table, like the newznab sibling's placeholder). Every
// preset shares the generic New factory; only its Definition differs — Prowlarr's
// pattern for its own Torznab.cs DefaultDefinitions (MoreThanTV, AnimeTosho, Torrent
// Network, and a Generic Torznab all share one Torznab class).
type preset struct {
	id      string
	name    string
	baseURL string
	apiPath string
	// language overrides the definition language ("" -> "en-US").
	language string
	// typ overrides the privacy classification ("" -> "private").
	typ        string
	keyPolicy  keyPolicy
	keyInfoURL string // the User Security / API-key page linked from the settings hint ("" -> no hint field)
	categories []int  // pass-through torznab category ids (already canonical — no tracker-id remap)
	// needsResolver reports whether the preset's download links carry URL credentials
	// (or are not known NOT to): true routes the served feed through the /dl proxy.
	// Decided per preset — see each entry's comment for the evidence.
	needsResolver bool
}

// presets is the torznab-family preset table, mirroring Prowlarr Torznab.cs
// DefaultDefinitions site-for-site (the generic entry lives in sites.go, matching the
// newznab sibling's presets.go/sites.go split).
var presets = []preset{
	{
		id:         "morethantv",
		name:       "MoreThanTV",
		baseURL:    "https://www.morethantv.me/",
		apiPath:    "/api/torznab",
		keyPolicy:  keyRequired32, // Jackett MoreThanTVAPI.ApplyConfiguration: "Expected length: 32"
		keyInfoURL: "https://www.morethantv.me/user/security",
		// TVSD, TVHD, TVUHD, TVSport, MoviesSD, MoviesHD, MoviesUHD, MoviesBluRay —
		// Jackett's MoreThanTVAPI.SetCapabilities category-mapping list, id-for-id.
		categories: []int{5030, 5040, 5045, 5060, 2030, 2040, 2045, 2050},
		// Evidence: the real capture's <link>/enclosure both embed authkey+torrent_pass.
		needsResolver: true,
	},
	{
		id:      "animetosho",
		name:    "AnimeTosho",
		baseURL: "https://feed.animetosho.org",
		// Prowlarr's GetSettings("https://feed.animetosho.org") does not override
		// ApiPath, so the NewznabSettings constructor default "/api" applies.
		apiPath:   "/api",
		typ:       "public",
		keyPolicy: keyNone, // public feed — no API key exists
		// {2020 Movies/Other, 5070 TV/Anime} — Prowlarr Torznab.cs DefaultDefinitions.
		categories: []int{2020, 5070},
		// Evidence for FALSE: the real capture (torznab_animetosho.xml) serves plain,
		// uncredentialed storage URLs (…/torrents/<id>.torrent) plus public
		// magneturl/infohash attrs — nothing to seal.
		needsResolver: false,
	},
	{
		id:      "torrentnetwork",
		name:    "Torrent Network",
		baseURL: "https://tntracker.org",
		apiPath: "/api/torznab/api", // Prowlarr GetSettings apiPath override
		// "GERMAN Private site for TV / MOVIES / GENERAL" — Prowlarr Torznab.cs.
		language:  "de-DE",
		keyPolicy: keyRequired, // private tracker; key length undocumented, so no 32-char rule
		// Prowlarr's TN preset seeds no categories (its caps come from a live fetch
		// harbrr's torznab driver doesn't perform); an empty seed advertises the full
		// standard parent table so any category search resolves (the same fallback
		// posture as the newznab sibling's placeholder).
		categories:    nil,
		needsResolver: true, // link shape unknown — sealing is the safe default (over-sealing is harmless; leaking is not)
	},
}

// presetByID resolves a preset by its definition id, used by profileFor to look up
// the site's fixed apiPath and per-preset policies.
func presetByID(id string) (preset, bool) {
	for _, p := range presets {
		if p.id == id {
			return p, true
		}
	}
	return preset{}, false
}

// Families returns the generic torznab entry plus every preset, each a native.Family
// sharing the New factory with its own Go-built, caps-only Definition. This is wired
// into the registry's native catalog (catalog.All()), so the generic indexer and all
// presets are addable like any other native family — Prowlarr's "one Torznab
// implementation + DefaultDefinitions" shape.
func Families() []native.Family {
	out := make([]native.Family, 0, len(presets)+1)
	out = append(out, Family())
	for _, p := range presets {
		out = append(out, native.Family{Definition: presetDefinition(p), Factory: New})
	}
	return out
}

// presetDefinition builds a preset's caps-only definition: torrent protocol, the
// preset's default base-URL link, its policy-derived settings, and the pass-through
// category caps. It is never schema-validated (no login/search/download block) — it
// exists so mapper.Build, the credential store, indexerInfo, and the addable-indexer
// list all work.
func presetDefinition(p preset) *loader.Definition {
	delay := requestDelaySeconds
	lang := p.language
	if lang == "" {
		lang = "en-US"
	}
	typ := p.typ
	if typ == "" {
		typ = "private"
	}
	return &loader.Definition{
		ID:           p.id,
		Name:         p.name,
		Description:  p.name + " (native Torznab driver)",
		Language:     lang,
		Type:         typ,
		Encoding:     "UTF-8",
		Protocol:     loader.ProtocolTorrent,
		Links:        []string{p.baseURL},
		RequestDelay: &delay,
		Settings:     presetSettingFields(p),
		Caps:         presetCaps(p.categories),
	}
}

// presetSettingFields derives a preset's user-entered fields from its key policy: a
// keyless public feed has no settings at all; a keyed preset gets the apikey secret
// field (the "apikey" token auto-classifies it — encrypted at rest, redacted by the
// API) plus a display-only key-page hint when the site's key page is known (Jackett's
// MoreThanTVAPI adds the identical dynamic field).
func presetSettingFields(p preset) []loader.SettingsField {
	if p.keyPolicy == keyNone {
		return nil
	}
	fields := []loader.SettingsField{
		{Name: "apikey", Label: "API Key", Type: "text"},
	}
	if p.keyInfoURL != "" {
		fields = append(fields, loader.SettingsField{
			Name:  "keyInfo",
			Label: "API Key",
			Type:  "info",
			Default: &loader.Scalar{
				Value: `Find or generate an API key on the ` + p.name + ` <a href="` + p.keyInfoURL + `" target="_blank">User Security</a> page, under the API Keys section.`,
				Set:   true,
			},
		})
	}
	return fields
}

// presetCaps builds the preset's caps. With no seed categories it falls back to the
// full standard parent table (placeholderCaps — the generic/TN posture: the remote
// server's real tree is unknown, so everything resolvable is advertised). With a seed
// list, each pass-through category id maps 1:1 to itself (Torznab items already
// report categories in the canonical vocabulary — no tracker-id remap needed, so
// unlike the newznab sibling's presetCaps this deliberately does NOT set
// CategoryMapping.Desc, so mapCategoryMappings does not synthesize a redundant custom
// (1:1 + 100000) entry for an id that is already canonical). The advertised modes are
// the three the driver's request generator expresses.
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
			ID:  loader.Scalar{Value: strconv.Itoa(id), Set: true},
			Cat: cat.Name,
		})
	}
	return loader.Caps{
		CategoryMappings:  mappings,
		Modes:             commonModes(),
		AllowTVSearchIMDB: &allowIMDB,
	}
}

// commonModes is the set of search modes the torznab request generator can express:
// t=search (q), t=tvsearch (q/season/ep/imdbid/tvdbid), t=movie (q/imdbid) — Jackett's
// MoreThanTVAPI param mapping. Music/book search are deliberately NOT advertised: the
// request generator has no artist/album/author params (no shipped torznab site needs
// them), and advertising a mode whose params would be silently dropped is worse than
// clean degradation to the modes that work.
func commonModes() loader.Modes {
	return loader.Modes{
		Search:      []string{"q"},
		TVSearch:    []string{"q", "season", "ep", "imdbid", "tvdbid"},
		MovieSearch: []string{"q", "imdbid"},
	}
}
