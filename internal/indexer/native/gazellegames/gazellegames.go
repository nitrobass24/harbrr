// Package gazellegames is the native driver for GazelleGames (gazellegames.net), a
// Gazelle-derived games/applications tracker. It has no Cardigann definition because its
// api.php JSON endpoint — an API key carried in the X-API-Key header, numerics
// wire-encoded as JSON strings, a nested group→torrents structure that flattens to one
// release per torrent, and a download URL rebuilt from a server-fetched passkey
// (torrents.php?action=download&torrent_pass=…) — exceeds the declarative format, so the
// search/parse/grab logic lives here in Go. The driver reproduces Prowlarr's documented
// contract (GazelleGames / GazelleGamesRequestGenerator / GazelleGamesParser) and reuses
// every harbrr seam (paced HTTP client, secret store, normalized release, caps mapper,
// the /dl grab proxy, redaction).
package gazellegames

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

// driver is one configured GazelleGames instance. It is built once per instance and
// cached by the registry. There is no login round-trip: every request carries the API
// key in the X-API-Key header, so the driver holds no session state. The download
// passkey is fetched on demand (request=quick_user) and persisted via persist.
type driver struct {
	def     *loader.Definition
	caps    *mapper.Capabilities
	cfg     map[string]string
	doer    search.Doer
	baseURL string // normalised with a single trailing slash
	clock   func() time.Time
	persist func(ctx context.Context, name, value string) error
}

var _ native.Driver = (*driver)(nil)

// New is the native.Factory for GazelleGames. It builds the capabilities from the
// definition, normalises the base URL, and defaults the clock.
func New(p native.Params) (native.Driver, error) {
	if p.Def == nil {
		return nil, errors.New("gazellegames: nil definition")
	}
	caps, err := mapper.Build(p.Def)
	if err != nil {
		return nil, fmt.Errorf("gazellegames: build capabilities for %q: %w", p.Def.ID, err)
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
		persist: p.PersistSetting,
	}, nil
}

// Capabilities returns the GazelleGames capabilities document.
func (d *driver) Capabilities() *mapper.Capabilities { return d.caps }

// NeedsResolver is always true: a GazelleGames download URL carries the passkey in its
// torrent_pass query param, which *arr must not see, so the served feed routes through
// the /dl proxy and the driver's Grab fetches the torrent server-side.
func (d *driver) NeedsResolver() bool { return true }

// DownloadNeedsAuth is false: GazelleGames is already routed through /dl by NeedsResolver,
// so the out-of-band-auth signal would be redundant (it mirrors FileList).
func (d *driver) DownloadNeedsAuth() bool { return false }

// Test verifies the configured API key authenticates (the management "test indexer"
// action). It issues a cheap latest-torrents search; a 401/403 surfaces from the search
// as login.ErrLoginFailed so the registry records an auth_failure health event.
func (d *driver) Test(ctx context.Context) error {
	_, err := d.Search(ctx, search.Query{})
	return err
}
