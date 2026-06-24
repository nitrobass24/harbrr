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
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/loader"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/mapper"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
	"github.com/autobrr/harbrr/internal/indexer/native"
)

// driver is one configured Gazelle-family instance. It is built once per instance and
// cached by the registry. There is no login round-trip: every request carries the API
// key in the Authorization header, so the driver holds no session state.
type driver struct {
	def     *loader.Definition
	caps    *mapper.Capabilities
	cfg     map[string]string
	doer    search.Doer
	baseURL string // normalised with a single trailing slash
	clock   func() time.Time
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

// New is the native.Factory for every Gazelle-family site. It builds the capabilities
// from the (per-site) definition, normalises the base URL, and resolves the per-site
// profile from the definition id.
func New(p native.Params) (native.Driver, error) {
	if p.Def == nil {
		return nil, errors.New("gazelle: nil definition")
	}
	caps, err := mapper.Build(p.Def)
	if err != nil {
		return nil, fmt.Errorf("gazelle: build capabilities for %q: %w", p.Def.ID, err)
	}
	base := p.BaseURL
	if base == "" && len(p.Def.Links) > 0 {
		base = p.Def.Links[0]
	}
	clock := p.Clock
	if clock == nil {
		clock = time.Now
	}
	return &driver{
		def:     p.Def,
		caps:    caps,
		cfg:     p.Cfg,
		doer:    p.Doer,
		baseURL: strings.TrimRight(base, "/") + "/",
		clock:   clock,
		profile: profileFor(p.Def.ID),
	}, nil
}

// Capabilities returns the per-site capabilities document.
func (d *driver) Capabilities() *mapper.Capabilities { return d.caps }

// NeedsResolver is false: the download URL (ajax.php?action=download&id=...) carries no
// passkey, so the served feed link is safe to expose. The Authorization header is added
// server-side at grab time, which DownloadNeedsAuth signals instead.
func (d *driver) NeedsResolver() bool { return false }

// DownloadNeedsAuth is true: the download authenticates out-of-band via the
// Authorization header, so the served feed routes through the /dl proxy and the
// driver's Grab fetches the torrent server-side with the header attached.
func (d *driver) DownloadNeedsAuth() bool { return true }

// Grab is implemented in a later leaf.
func (d *driver) Grab(_ context.Context, _ string) (*search.GrabResult, error) {
	return nil, errors.New("gazelle: grab not implemented")
}

// Test is implemented in a later leaf.
func (d *driver) Test(_ context.Context) error {
	return errors.New("gazelle: test not implemented")
}
