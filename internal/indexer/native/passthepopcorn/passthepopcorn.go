// Package passthepopcorn is the native driver for PassThePopcorn
// (passthepopcorn.me). It has no Cardigann definition because its JSON
// torrents.php?action=advanced search — two credentials (ApiUser, ApiKey) carried as
// request headers, a movie group whose nested torrents flatten to one release each,
// numerics wire-encoded as JSON strings, and a polymorphic torrent id (int-or-string) —
// exceeds the declarative format, so the search/parse/grab logic lives here in Go. The
// driver reproduces Prowlarr's documented contract (PassThePopcornRequestGenerator /
// PassThePopcornParser / PassThePopcornSettings) and reuses every harbrr seam (paced
// HTTP client, secret store, normalized release, caps mapper, the /dl grab proxy,
// redaction).
package passthepopcorn

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/loader"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/mapper"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/normalizer"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
	"github.com/autobrr/harbrr/internal/indexer/native"
)

// driver is one configured PassThePopcorn instance. It is built once per instance and
// cached by the registry. There is no login round-trip: every request carries the
// ApiUser/ApiKey credentials in request headers, so the driver holds no session state.
type driver struct {
	def     *loader.Definition
	caps    *mapper.Capabilities
	cfg     map[string]string
	doer    search.Doer
	baseURL string // normalised with a single trailing slash
	clock   func() time.Time
}

var _ native.Driver = (*driver)(nil)

// New is the native.Factory for PassThePopcorn. It builds the capabilities from the
// definition and normalises the base URL.
func New(p native.Params) (native.Driver, error) {
	if p.Def == nil {
		return nil, errors.New("passthepopcorn: nil definition")
	}
	caps, err := mapper.Build(p.Def)
	if err != nil {
		return nil, fmt.Errorf("passthepopcorn: build capabilities for %q: %w", p.Def.ID, err)
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

// Capabilities returns the PassThePopcorn capabilities document.
func (d *driver) Capabilities() *mapper.Capabilities { return d.caps }

// NeedsResolver is false: the download URL (torrents.php?action=download&id=...) carries
// no passkey, so the served feed link is safe to expose. The ApiUser/ApiKey headers are
// re-attached server-side at grab time, which DownloadNeedsAuth signals instead. (This
// matches the Gazelle model, not FileList/BroadcastTheNet — PTP's download URL embeds no
// secret; see PassThePopcornParser.GetDownloadUrl + PassThePopcorn.GetDownloadRequest.)
func (d *driver) NeedsResolver() bool { return false }

// DownloadNeedsAuth is true: the download authenticates out-of-band via the ApiUser/
// ApiKey headers, so the served feed routes through the /dl proxy and the driver's Grab
// fetches the torrent server-side with the headers attached.
func (d *driver) DownloadNeedsAuth() bool { return true }

// Search is implemented in a later leaf.
func (d *driver) Search(_ context.Context, _ search.Query) ([]*normalizer.Release, error) {
	return nil, errors.New("passthepopcorn: Search not implemented")
}

// Grab is implemented in a later leaf.
func (d *driver) Grab(_ context.Context, _ string) (*search.GrabResult, error) {
	return nil, errors.New("passthepopcorn: Grab not implemented")
}

// Test verifies the configured credentials authenticate (the management "test indexer"
// action). It is implemented in a later leaf alongside Search.
func (d *driver) Test(_ context.Context) error {
	return errors.New("passthepopcorn: Test not implemented")
}
