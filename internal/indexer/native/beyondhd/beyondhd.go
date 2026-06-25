// Package beyondhd is the native driver for BeyondHD (beyond-hd.me). It has no
// Cardigann definition because its search is a JSON POST to api/torrents/{api_key} —
// the api_key carried as a URL path segment (a BHD quirk), a separate rsskey carried as
// a body field on every request, a {status_code,status_message,results[]} envelope where
// status_code==0 is failure, and a download_url that embeds the rsskey — which exceeds
// the declarative format, so the search/parse/grab logic lives here in Go. The driver
// reproduces Prowlarr's documented contract (BeyondHDRequestGenerator / BeyondHDParser /
// BeyondHDSettings) and reuses every harbrr seam (paced HTTP client, secret store,
// normalized release, caps mapper, the /dl grab proxy, redaction).
package beyondhd

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

// driver is one configured BeyondHD instance. It is built once per instance and cached
// by the registry. There is no login round-trip: every request carries the api_key in
// the URL path and the rsskey in the JSON POST body, so the driver holds no session
// state.
type driver struct {
	def     *loader.Definition
	caps    *mapper.Capabilities
	cfg     map[string]string
	doer    search.Doer
	baseURL string // normalised with a single trailing slash
	clock   func() time.Time
}

var _ native.Driver = (*driver)(nil)

// New is the native.Factory for BeyondHD. It builds the capabilities from the definition
// and normalises the base URL.
func New(p native.Params) (native.Driver, error) {
	if p.Def == nil {
		return nil, errors.New("beyondhd: nil definition")
	}
	caps, err := mapper.Build(p.Def)
	if err != nil {
		return nil, fmt.Errorf("beyondhd: build capabilities for %q: %w", p.Def.ID, err)
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

// Capabilities returns the BeyondHD capabilities document.
func (d *driver) Capabilities() *mapper.Capabilities { return d.caps }

// NeedsResolver is always true: a BeyondHD download_url embeds the rsskey
// (torrent/download/auto.<id>.<rsskey>), which *arr must not see, so the served feed
// routes through the /dl proxy and the driver's Grab fetches the torrent server-side.
func (d *driver) NeedsResolver() bool { return true }

// DownloadNeedsAuth is false: the download URL already carries its own rsskey and is
// routed through /dl by NeedsResolver, so the out-of-band-auth signal would be redundant
// (it mirrors HDBits/BroadcastTheNet/FileList).
func (d *driver) DownloadNeedsAuth() bool { return false }

// Test verifies the configured credentials authenticate (the management "test indexer"
// action) by issuing an empty browse query: good credentials return status_code!=0, bad
// ones surface as login.ErrLoginFailed ("Invalid API Key" or HTTP 401/403).
func (d *driver) Test(ctx context.Context) error {
	_, err := d.Search(ctx, search.Query{})
	return err
}
