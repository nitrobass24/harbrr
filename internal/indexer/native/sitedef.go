package native

import "github.com/autobrr/harbrr/internal/indexer/cardigann/loader"

// Site is the data a native family declares per tracker. Definition() builds the
// caps-only loader.Definition every family used to hand-write: Type "private",
// Encoding "UTF-8", Language "en-US" unless set, Description
// "<Name> (native <Driver> driver)". The definition is never schema-validated (it
// has no login/search/download block); it exists so mapper.Build, the credential
// store (settingFields/IsSecret), indexerInfo, and the addable-indexer list all
// work for a native family with no special case.
type Site struct {
	ID, Name, Link string
	// Driver is the description's middle: "HDBits", "Gazelle-family", "HTML-scrape".
	Driver string
	// DelaySeconds is the between-request pacing, riding on the definition's
	// RequestDelay so the registry's existing paced client enforces it.
	DelaySeconds float64
	// Language is the definition language; "" means en-US.
	Language string
	Settings []loader.SettingsField
	Caps     loader.Caps
}

// Definition builds the family's caps-only loader.Definition from the site data.
func (s Site) Definition() *loader.Definition {
	delay := s.DelaySeconds
	lang := s.Language
	if lang == "" {
		lang = "en-US"
	}
	return &loader.Definition{
		ID:           s.ID,
		Name:         s.Name,
		Description:  s.Name + " (native " + s.Driver + " driver)",
		Language:     lang,
		Type:         "private",
		Encoding:     "UTF-8",
		Links:        []string{s.Link},
		RequestDelay: &delay,
		Settings:     s.Settings,
		Caps:         s.Caps,
	}
}

// Cat is one category row. Named fields make the argument order unwritable — the
// per-family cat/catDesc helpers this replaces had four positional signatures
// behind two names, one pair with opposite meaning. Newznab is the standard
// newznab category name (loader.CategoryMapping.Cat, resolved to a newznab id by
// mapper.GetByName); Desc is the tracker's own human label (a desc additionally
// synthesises Jackett's custom 1:1 category — see mapper.mapCategoryMappings).
type Cat struct{ ID, Newznab, Desc string }

// Cats builds a family's category-mapping table from Cat rows, in order.
func Cats(rows ...Cat) []loader.CategoryMapping {
	out := make([]loader.CategoryMapping, 0, len(rows))
	for _, r := range rows {
		out = append(out, loader.CategoryMapping{
			ID:   loader.Scalar{Value: r.ID, Set: true},
			Cat:  r.Newznab,
			Desc: r.Desc,
		})
	}
	return out
}

// The credential fields families compose into a Site's Settings, each label/type
// spelled once (the majority spelling of the per-family literals this replaces).
// Secret classification is by loader.SettingsField.IsSecret: a name carrying a
// credential token (apikey/passkey/cookie/…) or a "password" type is encrypted at
// rest and redacted by the API. A family with an odd spelling (hdbits' username is
// force-typed "password" to make it a secret) keeps an inline literal instead.
var (
	FieldUsername          = loader.SettingsField{Name: "username", Label: "Username", Type: "text"}
	FieldPassword          = loader.SettingsField{Name: "password", Label: "Password", Type: "password"}
	FieldAPIKey            = loader.SettingsField{Name: "apikey", Label: "API Key", Type: "text"}
	FieldPasskey           = loader.SettingsField{Name: "passkey", Label: "Passkey", Type: "text"}
	FieldCookie            = loader.SettingsField{Name: "cookie", Label: "Cookie", Type: "text"}
	FieldUserAgent         = loader.SettingsField{Name: "user_agent", Label: "User-Agent", Type: "text"}
	FieldPID               = loader.SettingsField{Name: "pid", Label: "PID", Type: "password"}
	FieldFreeleechOnly     = loader.SettingsField{Name: "freeleech_only", Label: "Only freeleech", Type: "checkbox"}
	FieldUseFreeleechToken = loader.SettingsField{Name: "use_freeleech_token", Label: "Use freeleech token", Type: "checkbox"}
)
