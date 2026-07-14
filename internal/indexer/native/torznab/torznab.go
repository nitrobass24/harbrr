// Package torznab is the native driver for the torrent-protocol twin of the Newznab
// Torznab API: a tracker whose own search endpoint speaks the Torznab RSS/XML contract
// directly, rather than exposing a Cardigann-compatible HTML/JSON surface. It has no
// Cardigann definition for the same reason the newznab sibling has none — the wire
// format is protocol-level, not a YAML corpus entry — but the acquisition protocol is
// torrent, not usenet: seeders/leechers/peers are meaningful, downloads are
// credentialed .torrent URLs (never a bare apikey-only link), and DVF/UVF carry the
// tracker's freeleech economy. See docs/native-indexer-pattern.md for the hard rule
// this driver exists to enforce: a torrent tracker exposing Torznab is NEVER served by
// the newznab driver, even though the wire format would parse.
//
// The first (and currently only) preset is MoreThanTV (morethantv.me), whose
// MoreThanTVAPI.cs (Jackett) is a thin BaseNewznabIndexer subclass with two
// overrides — the x-bittorrent enclosure beats <link>, and seeders/peers/DVF/UVF get
// default values when the feed omits them — layered on the shared Torznab item
// mapping Prowlarr's TorznabRssParser also implements. This driver reproduces that
// contract and reuses every harbrr seam (paced HTTP doer, the secret store, the
// normalized release, the caps mapper, the /dl grab proxy, URL redaction).
package torznab

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/autobrr/harbrr/internal/indexer/native"
)

// apikeyLength is the fixed API key length MoreThanTV (and every current torznab
// preset) issues. Jackett's MoreThanTVAPI.ApplyConfiguration rejects any other length
// at add-time ("Invalid API Key configured. Expected length: 32"); harbrr enforces the
// same check at driver construction so a misconfigured key fails loudly and early
// rather than as an opaque 401 on the first search.
const apikeyLength = 32

// defaultAPIPath is the fallback API path used only if a definition id is ever built
// outside the preset table (unreachable in practice: every family this package exports
// comes from Families(), whose ids are always resolvable via presetByID).
const defaultAPIPath = "/api"

// driver is one configured torznab-family instance. It is built once per instance and
// cached by the registry. There is no login round-trip: every request carries the
// apikey as a query param, so the driver holds no session state. apiPath is resolved
// once at construction from the preset table (it is not a user-facing setting — only
// the apikey is, per the MoreThanTV preset's single secret field).
type driver struct {
	native.Base
	apikey  string
	apiPath string // normalised, no trailing slash (e.g. "/api/torznab")
}

var _ native.Driver = (*driver)(nil)

// New is the native.Factory shared by every torznab-family preset. It validates the
// configured apikey's length before building the transport scaffold, so a
// misconfigured instance never issues a single request.
func New(p native.Params) (native.Driver, error) {
	if p.Def == nil {
		return nil, errors.New("torznab: nil definition")
	}
	apikey := strings.TrimSpace(p.Cfg["apikey"])
	if len(apikey) != apikeyLength {
		return nil, fmt.Errorf("torznab: invalid API key configured for %q: expected length %d, got %d", p.Def.ID, apikeyLength, len(apikey))
	}
	base, err := native.NewBase("torznab", p)
	if err != nil {
		return nil, err
	}
	apiPath := defaultAPIPath
	if pr, ok := presetByID(p.Def.ID); ok {
		apiPath = pr.apiPath
	}
	return &driver{Base: base, apikey: apikey, apiPath: apiPath}, nil
}

// NeedsResolver is always true: a torznab download URL carries authkey+torrent_pass in
// its query (the Gazelle-style URL-credential shape — evidence: the real MoreThanTV
// capture's <link>/enclosure both embed them), which *arr must not see, so the served
// feed routes through the /dl proxy and the driver's Grab fetches the torrent
// server-side.
func (d *driver) NeedsResolver() bool { return true }

// DownloadNeedsAuth is false: the download URL is already fully self-authenticating
// (authkey+torrent_pass ride the URL itself), so the out-of-band-auth signal would be
// redundant — the same posture as FileList and GazelleGames.
func (d *driver) DownloadNeedsAuth() bool { return false }

// Test verifies the configured apikey authenticates (the management "test indexer"
// action) via a cheap empty search; a non-XML body or a 401/403 both surface as
// login.ErrLoginFailed so the registry records an auth_failure health event.
func (d *driver) Test(ctx context.Context) error {
	return native.TestViaSearch(ctx, d)
}
