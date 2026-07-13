// Package gazelle is the native driver for the Gazelle-family music trackers
// Redacted (redacted.sh) and Orpheus (orpheus.network). They have no Cardigann
// definition because their ajax.php?action=browse API — an API key carried in the
// Authorization header (RED bare, OPS "token "-prefixed), numerics wire-encoded as
// JSON strings, a music group whose nested torrents flatten to one release each, and a
// header-authenticated action=download grab with a freeleech-token fallback — exceeds
// the declarative format, so the search/parse/grab logic lives here in Go. The driver
// reproduces Prowlarr's documented contract (GazelleApi / GazelleParser /
// Redacted / Orpheus) and reuses every harbrr seam (paced HTTP client, secret store,
// normalized release, caps mapper, the /dl grab proxy, redaction).
package gazelle

import (
	"context"

	"github.com/autobrr/harbrr/internal/indexer/native"
)

// driver is one configured Gazelle-family instance. It is built once per instance and
// cached by the registry. There is no login round-trip: every request carries the API
// key in the Authorization header, so the driver holds no session state. Everything but
// the Gazelle request/parse dialect and the per-site profile lives in the embedded
// native.Base.
type driver struct {
	native.Base
	profile profile
}

var _ native.Driver = (*driver)(nil)

// profile captures the per-site behaviour that differs across the two sites, keyed off
// the definition id. authPrefix is the Authorization header value prefix — "" for RED
// (bare apikey) and "token " for OPS. OPS additionally must never send usetoken=0 (it
// fails the download); the driver omits the param entirely when off, so the only
// per-site auth difference modelled here is the header prefix.
type profile struct {
	site       string
	authPrefix string
}

func profileFor(id string) profile {
	if id == "orpheus" {
		return profile{site: id, authPrefix: "token "}
	}
	return profile{site: id, authPrefix: ""}
}

// New is the native.Factory for every Gazelle-family site. The per-site profile is
// resolved from the definition id.
func New(p native.Params) (native.Driver, error) {
	b, err := native.NewBase("gazelle", p)
	if err != nil {
		return nil, err
	}
	return &driver{Base: b, profile: profileFor(p.Def.ID)}, nil
}

// NeedsResolver is false: the download URL (ajax.php?action=download&id=...) carries no
// passkey, so the served feed link is safe to expose. The Authorization header is added
// server-side at grab time, which DownloadNeedsAuth signals instead.
func (d *driver) NeedsResolver() bool { return false }

// DownloadNeedsAuth is true: the download authenticates out-of-band via the
// Authorization header, so the served feed routes through the /dl proxy and the
// driver's Grab fetches the torrent server-side with the header attached.
func (d *driver) DownloadNeedsAuth() bool { return true }

// Test exercises the credentials with an empty browse query: a 401/403 surfaces as
// login.ErrLoginFailed (the registry records an auth_failure health event), while a
// parseable empty page confirms the key works.
func (d *driver) Test(ctx context.Context) error {
	return native.TestViaSearch(ctx, d)
}
