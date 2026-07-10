// Package torrentday is the native driver for TorrentDay (TD). TorrentDay has no
// Cardigann definition because its search surface is a JSON endpoint (GET /t.json)
// whose tracker categories are encoded path-style in the request (`?29;28;q=term`)
// and whose rows wire-encode numerics as either JSON numbers or strings — logic the
// declarative YAML format cannot express. The driver reproduces Prowlarr's documented
// contract (TorrentDay.cs: TorrentDayRequestGenerator / TorrentDayParser /
// TorrentDaySettings) and reuses every harbrr seam (paced HTTP client, secret store,
// normalized release, caps mapper, the /dl grab proxy, redaction).
//
// Auth is a session cookie: the user pastes the full browser Cookie string
// (uid=...; pass=...), sent as the Cookie header on every request — no login
// round-trip, exactly like IPTorrents. Because *arr cannot send that cookie, the
// cookie-authenticated download (download.php/<id>/<id>.torrent) routes through the
// /dl proxy (NeedsResolver) where this driver's Grab fetches the .torrent
// server-side.
package torrentday

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/rs/zerolog"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/loader"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/mapper"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
	"github.com/autobrr/harbrr/internal/indexer/native"
)

// driver is one configured TorrentDay instance. It is built once per instance and
// cached by the registry. There is no token to refresh — the session cookie is static
// config, sent as the Cookie header on every request.
type driver struct {
	def     *loader.Definition
	caps    *mapper.Capabilities
	cfg     map[string]string
	doer    search.Doer
	baseURL string // normalised with a single trailing slash
	clock   func() time.Time
	log     zerolog.Logger
}

var _ native.Driver = (*driver)(nil)

// New is the native.Factory for TorrentDay. It builds the capabilities from the
// Go-built definition and normalises the base URL.
func New(p native.Params) (native.Driver, error) {
	if p.Def == nil {
		return nil, errors.New("torrentday: nil definition")
	}
	caps, err := mapper.Build(p.Def)
	if err != nil {
		return nil, fmt.Errorf("torrentday: build capabilities for %q: %w", p.Def.ID, err)
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
		log:     p.Logger,
	}, nil
}

// Capabilities returns the TorrentDay capabilities document.
func (d *driver) Capabilities() *mapper.Capabilities { return d.caps }

// NeedsResolver is always true: a TorrentDay download must be fetched with the session
// cookie *arr cannot send, so the served feed routes through the /dl proxy and the
// driver's Grab fetches the torrent server-side.
func (d *driver) NeedsResolver() bool { return true }

// DownloadNeedsAuth is false: TorrentDay is already routed through /dl by
// NeedsResolver, so the out-of-band-auth signal would be redundant (it mirrors
// IPTorrents/FileList).
func (d *driver) DownloadNeedsAuth() bool { return false }
