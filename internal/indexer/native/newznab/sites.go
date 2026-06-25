package newznab

import (
	"github.com/autobrr/harbrr/internal/indexer/cardigann/loader"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/mapper"
	"github.com/autobrr/harbrr/internal/indexer/native"
)

// requestDelaySeconds is the conservative between-request pacing for a generic Newznab
// server. Prowlarr applies no fixed RateLimit to the generic Newznab indexer (limits are
// the remote server's concern); harbrr rides a small RequestDelay on the definition so the
// registry's paced client never hammers an unknown server.
const requestDelaySeconds = 2.0

// defaultAPIPath is the Newznab API base path (Prowlarr NewznabSettings default "/api").
const defaultAPIPath = "/api"

// Family builds the generic Newznab native family. It carries a Go-built, caps-only
// definition (id/name/type/links/settings + a STANDARD-newznab-category placeholder caps)
// and the New factory. The caps are a placeholder: Leaf 5 replaces them with a live
// ?t=caps fetch that maps the remote category tree onto the standard table. The base URL
// is empty here — a generic Newznab instance is configured with its own base URL at add
// time; presets (Leaf 7) carry a default link.
//
// Leaf 4 ships only the generic driver; the family is not yet wired into nativeFamilies()
// (that is Leaf 7, together with the ~18 presets), so the registry does not surface it yet.
func Family() native.Family {
	return native.Family{Definition: GenericDefinition(), Factory: New}
}

// GenericDefinition is the generic Newznab family definition. Protocol is usenet (the one
// native family that is not torrent), so the serializer omits torrent-only fields and the
// normalizer relaxes the seeders-required validation (Leaves 2 & 3). It is never
// schema-validated (no login/search/download block) — it exists so mapper.Build, the
// credential store (IsSecret), indexerInfo, and the addable-indexer list all work.
func GenericDefinition() *loader.Definition {
	delay := requestDelaySeconds
	return &loader.Definition{
		ID:           "newznab",
		Name:         "Generic Newznab",
		Description:  "Generic Newznab usenet indexer (native driver)",
		Language:     "en-US",
		Type:         "private",
		Encoding:     "UTF-8",
		Protocol:     loader.ProtocolUsenet,
		RequestDelay: &delay,
		Settings:     settingFields(),
		Caps:         placeholderCaps(),
	}
}

// settingFields are the user-entered fields. apikey carries the "apikey" token so the
// secret store auto-classifies it (encrypted at rest, redacted by the API). apiPath is a
// non-secret URL base with the Newznab "/api" default; it is a plain text field.
func settingFields() []loader.SettingsField {
	return []loader.SettingsField{
		{Name: "apikey", Label: "API Key", Type: "text"},
		{Name: "apiPath", Label: "API Path", Type: "text", Default: &loader.Scalar{Value: defaultAPIPath, Set: true}},
	}
}

// placeholderCaps is the STANDARD newznab category set, used until Leaf 5 fetches the
// remote server's real ?t=caps. Every standard top-level category (1000..8000) is mapped
// 1:1 to itself so the driver advertises the full table and search-time category mapping
// resolves a known set; the remote-id round-trip is identity for these. All five search
// modes are advertised with the common params so callers may issue any search; the remote
// server's own caps gate which params it actually honors (a follow-up once Leaf 5 lands).
func placeholderCaps() loader.Caps {
	allowIMDB := true
	mappings := make([]loader.CategoryMapping, 0)
	for _, c := range mapper.StandardCategories() {
		if !c.IsParent() {
			continue
		}
		mappings = append(mappings, loader.CategoryMapping{
			ID:   loader.Scalar{Value: itoa(c.ID), Set: true},
			Cat:  c.Name,
			Desc: c.Name,
		})
	}
	return loader.Caps{
		CategoryMappings:  mappings,
		Modes:             commonModes(),
		AllowTVSearchIMDB: &allowIMDB,
	}
}

// commonModes is the full set of advertised search modes, with the common Newznab params
// per mode. The generic family and every preset advertise this same set; the remote
// server's own caps (fetched at Leaf 5) gate which params it actually honors.
func commonModes() loader.Modes {
	return loader.Modes{
		Search:      []string{"q"},
		TVSearch:    []string{"q", "season", "ep", "imdbid", "tvdbid", "tvmazeid", "rid", "traktid"},
		MovieSearch: []string{"q", "imdbid", "tmdbid", "traktid"},
		MusicSearch: []string{"q", "artist", "album"},
		BookSearch:  []string{"q", "author", "title"},
	}
}
