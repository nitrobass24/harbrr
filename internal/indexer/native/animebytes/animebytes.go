// Package animebytes is the native driver for AnimeBytes (animebytes.tv). It has no
// Cardigann definition because its scrape.php JSON API — a username + passkey carried
// in the request query (so the search URL itself is secret-bearing), a nested
// group→torrent structure that flattens to one release per torrent, a synthesized
// title composed from the group name plus the torrent Property descriptor, numerics
// wire-encoded as either JSON strings or numbers, and a download URL that embeds the
// passkey — exceeds the declarative format, so the search/parse/grab logic lives here in
// Go. The driver reproduces Prowlarr's documented contract (AnimeBytesRequestGenerator
// / AnimeBytesParser / AnimeBytesSettings) and reuses every harbrr seam (paced HTTP
// client, secret store, normalized release, caps mapper, the /dl grab proxy,
// redaction).
package animebytes

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

// driver is one configured AnimeBytes instance. It is built once per instance and
// cached by the registry. There is no login round-trip: every request carries the
// username + passkey in the scrape.php query, so the driver holds no session state.
type driver struct {
	def     *loader.Definition
	caps    *mapper.Capabilities
	cfg     map[string]string
	doer    search.Doer
	baseURL string // normalised with a single trailing slash
	clock   func() time.Time
}

var _ native.Driver = (*driver)(nil)

// New is the native.Factory for AnimeBytes. It builds the capabilities from the
// definition and normalises the base URL.
func New(p native.Params) (native.Driver, error) {
	if p.Def == nil {
		return nil, errors.New("animebytes: nil definition")
	}
	caps, err := mapper.Build(p.Def)
	if err != nil {
		return nil, fmt.Errorf("animebytes: build capabilities for %q: %w", p.Def.ID, err)
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
	}, nil
}

// Capabilities returns the AnimeBytes capabilities document.
func (d *driver) Capabilities() *mapper.Capabilities { return d.caps }

// NeedsResolver is always true: an AnimeBytes download URL embeds the passkey in its
// path/query, which *arr must not see, so the served feed routes through the /dl proxy
// and the driver's Grab fetches the torrent server-side.
func (d *driver) NeedsResolver() bool { return true }

// DownloadNeedsAuth is false: the download URL already carries its own passkey and is
// routed through /dl by NeedsResolver, so the out-of-band-auth signal would be redundant
// (it mirrors FileList/BroadcastTheNet/AvistaZ).
func (d *driver) DownloadNeedsAuth() bool { return false }

// Test verifies the configured credentials authenticate (the management "test indexer"
// action). It issues a cheap empty scrape query; a 401/403 or the JSON {"error":...}
// envelope surfaces as a search error.
func (d *driver) Test(ctx context.Context) error {
	_, err := d.Search(ctx, search.Query{})
	return err
}

// Grab is implemented in a later leaf; the skeleton returns a not-implemented error.
func (d *driver) Grab(_ context.Context, _ string) (*search.GrabResult, error) {
	return nil, errors.New("animebytes: grab not implemented")
}
