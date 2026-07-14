// Package torznab is the native driver for the torrent-protocol twin of the Newznab
// Torznab API: a tracker whose own search endpoint speaks the Torznab RSS/XML contract
// directly, rather than exposing a Cardigann-compatible HTML/JSON surface. It has no
// Cardigann definition for the same reason the newznab sibling has none — the wire
// format is protocol-level, not a YAML corpus entry — but the acquisition protocol is
// torrent, not usenet: seeders/leechers/peers are meaningful, downloads may be
// credentialed .torrent URLs, and DVF/UVF carry the tracker's freeleech economy. See
// docs/native-indexer-pattern.md for the hard rule this driver exists to enforce: a
// torrent tracker exposing Torznab is NEVER served by the newznab driver, even though
// the wire format would parse.
//
// The family carries a generic user-supplied-URL entry plus the presets Prowlarr's
// Torznab.cs ships as DefaultDefinitions: MoreThanTV (whose Jackett MoreThanTVAPI.cs
// is the parser/request reference — the x-bittorrent enclosure beats <link>, and
// seeders/peers/DVF/UVF get default values when the feed omits them), AnimeTosho
// (public, keyless), and Torrent Network. This driver reproduces that contract and
// reuses every harbrr seam (paced HTTP doer, the secret store, the normalized
// release, the caps mapper, the /dl grab proxy, URL redaction).
package torznab

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/autobrr/harbrr/internal/indexer/native"
)

// apikeyLength is the fixed API key length MoreThanTV issues. Jackett's
// MoreThanTVAPI.ApplyConfiguration rejects any other length at add-time ("Invalid API
// Key configured. Expected length: 32"); harbrr enforces the same check at driver
// construction (keyRequired32) so a misconfigured key fails loudly and early rather
// than as an opaque 401 on the first search. It is MoreThanTV-specific — the other
// presets and the generic entry do not length-validate (Prowlarr's posture).
const apikeyLength = 32

// driver is one configured torznab-family instance. It is built once per instance and
// cached by the registry. There is no login round-trip: every request carries the
// apikey (when the site has one) as a query param, so the driver holds no session
// state. apiPath and needsResolver are resolved once at construction from the
// per-site profile.
type driver struct {
	native.Base
	apikey        string
	apiPath       string // normalised, no trailing slash (e.g. "/api/torznab")
	needsResolver bool
}

var _ native.Driver = (*driver)(nil)

// profile is the per-site behaviour New resolves from the definition id: the fixed
// API path (empty means the cfg-driven apiPath setting — the generic entry), the
// apikey policy, and the download-sealing posture.
type profile struct {
	apiPath       string
	policy        keyPolicy
	needsResolver bool
}

// profileFor resolves a definition id against the preset table. Anything not in the
// table is the generic entry ("torznab"): optional unvalidated key, cfg-driven
// apiPath, and sealed downloads (an unknown server's links are not known NOT to carry
// credentials, so over-sealing is the safe default).
func profileFor(id string) profile {
	if pr, ok := presetByID(id); ok {
		return profile{apiPath: pr.apiPath, policy: pr.keyPolicy, needsResolver: pr.needsResolver}
	}
	return profile{policy: keyOptional, needsResolver: true}
}

// New is the native.Factory shared by the generic entry and every preset. It
// validates the configured apikey per the site's policy before building the transport
// scaffold, so a misconfigured instance never issues a single request.
func New(p native.Params) (native.Driver, error) {
	if p.Def == nil {
		return nil, errors.New("torznab: nil definition")
	}
	prof := profileFor(p.Def.ID)
	apikey := strings.TrimSpace(p.Cfg["apikey"])
	if err := validateAPIKey(prof.policy, p.Def.ID, apikey); err != nil {
		return nil, err
	}
	if prof.policy == keyNone {
		// A keyless public feed: drop any stray configured value so it never rides a
		// request (there is no apikey setting to have set it through anyway).
		apikey = ""
	}
	baseParams := p
	if baseParams.BaseURL == "" && len(p.Def.Links) == 0 {
		// The generic entry intentionally has no default URL; keep it constructible
		// for metadata/caps reads until an instance supplies BaseURL (the newznab
		// sibling's idiom).
		baseParams.BaseURL = "https://torznab.invalid"
	}
	base, err := native.NewBase("torznab", baseParams)
	if err != nil {
		return nil, err
	}
	apiPath := prof.apiPath
	if apiPath == "" {
		apiPath = normalizeAPIPath(p.Cfg["apiPath"])
	}
	return &driver{Base: base, apikey: apikey, apiPath: apiPath, needsResolver: prof.needsResolver}, nil
}

// validateAPIKey enforces a site's key policy at construction: keyRequired is
// non-empty (length undocumented — Prowlarr validates nothing, so neither does
// harbrr); keyRequired32 is MoreThanTV's exact-32 Jackett rule; keyOptional and
// keyNone accept anything. Errors are clear and secret-free (a length, never a
// value).
func validateAPIKey(policy keyPolicy, defID, apikey string) error {
	if policy == keyRequired && apikey == "" {
		return fmt.Errorf("torznab: %q requires an API key and none is configured", defID)
	}
	if policy == keyRequired32 && len(apikey) != apikeyLength {
		return fmt.Errorf("torznab: invalid API key configured for %q: expected length %d, got %d", defID, apikeyLength, len(apikey))
	}
	return nil
}

// normalizeAPIPath resolves the generic entry's apiPath setting: a blank value
// defaults to "/api" (Prowlarr's NewznabSettings default, inherited by
// TorznabSettings); a trailing slash is stripped; a missing leading slash is added so
// {base}{apiPath} joins correctly — the newznab sibling's idiom.
func normalizeAPIPath(raw string) string {
	p := strings.TrimSpace(raw)
	if p == "" {
		p = defaultAPIPath
	}
	p = strings.TrimRight(p, "/")
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	return p
}

// NeedsResolver is per-site: true when the site's download links carry URL
// credentials, or are not known not to (MoreThanTV: authkey+torrent_pass in the query
// per the real capture; Torrent Network and the generic entry: unknown, sealed as the
// safe default) — *arr must not see such a link, so the served feed routes through
// the /dl proxy and the driver's Grab fetches the torrent server-side. AnimeTosho is
// the false case: its real capture serves plain uncredentialed storage URLs and
// public magnets, so its links are served bare.
func (d *driver) NeedsResolver() bool { return d.needsResolver }

// DownloadNeedsAuth is false for the whole family: no torznab site authenticates the
// download out-of-band (header/cookie) — a credentialed link is already fully
// self-authenticating and sealed by NeedsResolver instead, the same posture as
// FileList and GazelleGames.
func (d *driver) DownloadNeedsAuth() bool { return false }

// Test verifies the configured instance works (the management "test indexer" action)
// via a cheap empty search; a non-XML body or a 401/403 both surface as
// login.ErrLoginFailed so the registry records an auth_failure health event.
func (d *driver) Test(ctx context.Context) error {
	return native.TestViaSearch(ctx, d)
}
