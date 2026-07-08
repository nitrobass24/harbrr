package login

import (
	"strings"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/loader"
)

// secretRedaction is the placeholder a value-scrubbed credential is replaced with,
// matching the sibling native drivers (gazelle.scrubAPIKey et al).
const secretRedaction = "[redacted]"

// SecretConfigValues returns the non-empty config values of every setting the
// AUTHORITATIVE loader classifier (SettingsField.IsSecret) marks as a credential,
// so a server-echoed secret can be value-scrubbed out of an error message.
//
// Deriving the scrub set from IsSecret over the definition's OWN settings — rather
// than a hardcoded key list — is what makes it correct: it catches every credential
// the loader encrypts at rest (password/cookie/apikey/passkey/rsskey/authkey/2fa/
// otp/token/pin, AND a def's differently-named field such as Bittorrentfiles' `pass`,
// type: password), and it never scrubs a non-secret (username stays intact, so a
// legitimate "no such user 'dave'" survives). The empty-guard drops unset credentials
// so a later ReplaceAll is never handed "" (which would splice the placeholder
// between every rune).
//
// This is intentionally scoped to def.Settings, not the broader at-rest classifier
// (registry.classifySecret, which also flags proxy_url/flaresolverr_url and an
// undeclared-name fallback). Only a value the def actually SENT to the tracker can be
// echoed back, and every tracker-submitted secret is a def.Settings field — the infra
// keys (proxy_url/flaresolverr_url) are never referenced by a login/search template,
// so the tracker never receives them and cannot echo them.
func SecretConfigValues(settings []loader.SettingsField, config map[string]string) []string {
	var vals []string
	for i := range settings {
		if !settings[i].IsSecret() {
			continue
		}
		if v := config[settings[i].Name]; v != "" {
			vals = append(vals, v)
		}
	}
	return vals
}

// ScrubSecrets replaces every secret with the redaction placeholder. The values
// come from SecretConfigValues, which drops empties, so ReplaceAll is never asked
// to replace "".
//
// The scrub is a raw-value ReplaceAll, matching every sibling driver. A URL-encoded
// or case-folded echo of the secret would not match — a known limitation shared
// across all value-scrub sites — but a login/search page that echoes a submitted
// field back does so verbatim.
func ScrubSecrets(s string, secrets []string) string {
	for _, v := range secrets {
		s = strings.ReplaceAll(s, v, secretRedaction)
	}
	return s
}
