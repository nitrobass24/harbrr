// Package hdbits is the native driver for HDBits (hdbits.org). It has no Cardigann
// definition because its search is a JSON POST to api/torrents — username and passkey
// carried as top-level fields inside the request body, a typed TorrentQuery body, a
// {status,message,data[]} envelope where status==0 is success, and a download URL that
// embeds the passkey — which exceeds the declarative format, so the search/parse/grab
// logic lives here in Go. The driver reproduces Prowlarr's documented contract
// (HDBitsRequestGenerator / HDBitsParser / HDBitsSettings) and reuses every harbrr seam
// (paced HTTP client, secret store, normalized release, caps mapper, the /dl grab proxy,
// redaction).
package hdbits

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

// driver is one configured HDBits instance. It is built once per instance and cached by
// the registry. There is no login round-trip: every request carries the username and
// passkey as top-level fields inside the JSON POST body, so the driver holds no session
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

// New is the native.Factory for HDBits. It builds the capabilities from the definition
// and normalises the base URL.
func New(p native.Params) (native.Driver, error) {
	if p.Def == nil {
		return nil, errors.New("hdbits: nil definition")
	}
	caps, err := mapper.Build(p.Def)
	if err != nil {
		return nil, fmt.Errorf("hdbits: build capabilities for %q: %w", p.Def.ID, err)
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

// Capabilities returns the HDBits capabilities document.
func (d *driver) Capabilities() *mapper.Capabilities { return d.caps }

// NeedsResolver is always true: an HDBits download URL embeds the passkey in its query
// (download.php?id=…&passkey=…), which *arr must not see, so the served feed routes
// through the /dl proxy and the driver's Grab fetches the torrent server-side.
func (d *driver) NeedsResolver() bool { return true }

// DownloadNeedsAuth is false: the download URL already carries its own passkey and is
// routed through /dl by NeedsResolver, so the out-of-band-auth signal would be redundant
// (it mirrors FileList/BroadcastTheNet).
func (d *driver) DownloadNeedsAuth() bool { return false }

// Test verifies the configured credentials authenticate (the management "test indexer"
// action) by issuing an empty browse query: good credentials return status==0, bad ones
// surface as login.ErrLoginFailed (status 4/5 or HTTP 401/403).
func (d *driver) Test(ctx context.Context) error {
	_, err := d.Search(ctx, search.Query{})
	return err
}
