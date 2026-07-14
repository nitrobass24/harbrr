package torznab

import (
	"strconv"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/loader"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/mapper"
	"github.com/autobrr/harbrr/internal/indexer/native"
)

// requestDelaySeconds is the conservative between-request pacing for a torznab-family
// server. Prowlarr applies no fixed RateLimit to its Torznab indexer (limits are the
// remote server's concern); harbrr rides a small RequestDelay on the definition so
// the registry's paced client never hammers an unknown server — the same posture as
// the newznab sibling.
const requestDelaySeconds = 2.0

// defaultAPIPath is the Torznab API base path the generic entry defaults to
// (Prowlarr NewznabSettings constructor default "/api", which TorznabSettings
// inherits; a preset's fixed path lives on its table row instead).
const defaultAPIPath = "/api"

// Family builds the generic torznab native family: a user-supplied Torznab server
// (base URL + apiPath + optional apikey) — Prowlarr's "Generic Torznab"
// DefaultDefinitions entry, and the torrent twin of the newznab sibling's generic.
// The base URL is empty here — a generic instance is configured with its own base URL
// at add time; presets carry a default link.
func Family() native.Family {
	return native.Family{Definition: GenericDefinition(), Factory: New}
}

// GenericDefinition is the generic torznab family definition: torrent protocol, no
// default link, the apikey (optional, unvalidated) + apiPath settings, and the full
// standard parent table as placeholder caps (the remote server's real tree is
// unknown). It is never schema-validated (no login/search/download block) — it exists
// so mapper.Build, the credential store (IsSecret), indexerInfo, and the
// addable-indexer list all work.
func GenericDefinition() *loader.Definition {
	delay := requestDelaySeconds
	return &loader.Definition{
		ID:           "torznab",
		Name:         "Generic Torznab",
		Description:  "Generic Torznab torrent indexer (native driver)",
		Language:     "en-US",
		Type:         "private",
		Encoding:     "UTF-8",
		Protocol:     loader.ProtocolTorrent,
		RequestDelay: &delay,
		Settings:     genericSettingFields(),
		Caps:         placeholderCaps(),
	}
}

// genericSettingFields are the generic entry's user-entered fields, mirroring the
// newznab sibling's settingFields: apikey carries the "apikey" token so the secret
// store auto-classifies it (encrypted at rest, redacted by the API) — it is OPTIONAL
// and unvalidated here, since an unknown Torznab server may not require one; apiPath
// is a non-secret URL base with the "/api" default.
func genericSettingFields() []loader.SettingsField {
	return []loader.SettingsField{
		{Name: "apikey", Label: "API Key", Type: "text"},
		{Name: "apiPath", Label: "API Path", Type: "text", Default: &loader.Scalar{Value: defaultAPIPath, Set: true}},
	}
}

// placeholderCaps is the STANDARD parent category set, used by the generic entry and
// by presets that seed no categories (TN): every standard top-level category
// (1000..8000) is mapped 1:1 to itself so the driver advertises the full table and
// search-time category mapping resolves a known set — the newznab sibling's
// placeholder posture (harbrr's torznab driver performs no live ?t=caps fetch, so
// this is the standing advertised set, not a pre-fetch placeholder).
func placeholderCaps() loader.Caps {
	allowIMDB := true
	mappings := make([]loader.CategoryMapping, 0)
	for _, c := range mapper.StandardCategories() {
		if !c.IsParent() {
			continue
		}
		mappings = append(mappings, loader.CategoryMapping{
			ID:  loader.Scalar{Value: strconv.Itoa(c.ID), Set: true},
			Cat: c.Name,
		})
	}
	return loader.Caps{
		CategoryMappings:  mappings,
		Modes:             commonModes(),
		AllowTVSearchIMDB: &allowIMDB,
	}
}
