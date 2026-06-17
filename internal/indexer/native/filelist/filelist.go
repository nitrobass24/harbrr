// Package filelist is the native driver for FileList (filelist.io). It has no
// Cardigann definition because its HTTP Basic auth (username + passkey) over a JSON
// api.php endpoint, with the download URL rebuilt from the passkey, exceeds the
// declarative format, so the search/parse/grab logic lives here in Go. The driver
// reproduces Prowlarr's documented contract (FileListRequestGenerator /
// FileListParser / FileListSettings) and reuses every harbrr seam (paced HTTP
// client, secret store, normalized release, caps mapper, the /dl grab proxy,
// redaction).
package filelist

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

// driver is one configured FileList instance. It is built once per instance and
// cached by the registry. There is no login round-trip: every request carries the
// Authorization: Basic header built from the username + passkey, so the driver holds
// no session state.
type driver struct {
	def     *loader.Definition
	caps    *mapper.Capabilities
	cfg     map[string]string
	doer    search.Doer
	baseURL string // normalised with a single trailing slash
	clock   func() time.Time
}

var _ native.Driver = (*driver)(nil)

// New is the native.Factory for FileList. It builds the capabilities from the
// definition and normalises the base URL.
func New(p native.Params) (native.Driver, error) {
	if p.Def == nil {
		return nil, errors.New("filelist: nil definition")
	}
	caps, err := mapper.Build(p.Def)
	if err != nil {
		return nil, fmt.Errorf("filelist: build capabilities for %q: %w", p.Def.ID, err)
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

// Capabilities returns the FileList capabilities document.
func (d *driver) Capabilities() *mapper.Capabilities { return d.caps }

// NeedsResolver is always true: a FileList download URL carries the passkey in its
// query, which *arr must not see, so the served feed routes through the /dl proxy and
// the driver's Grab fetches the torrent server-side over Basic auth.
func (d *driver) NeedsResolver() bool { return true }

// DownloadNeedsAuth is false: FileList is already routed through /dl by NeedsResolver,
// so the out-of-band-auth signal would be redundant (it mirrors AvistaZ).
func (d *driver) DownloadNeedsAuth() bool { return false }

// Test verifies the configured credentials authenticate (the management
// "test indexer" action). It issues a cheap latest-torrents search; a 401/403 is an
// auth failure surfaced by the search.
func (d *driver) Test(ctx context.Context) error {
	_, err := d.Search(ctx, search.Query{})
	return err
}
