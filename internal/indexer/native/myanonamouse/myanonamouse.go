// Package myanonamouse is the native driver for MyAnonamouse (MAM). It has no
// Cardigann definition because its auth is a rotating `mam_id` session cookie sent
// on every request (the value is refreshed by the server's Set-Cookie on each
// response), which exceeds the declarative format — so the search/parse/grab logic
// lives here in Go. The driver reproduces Prowlarr's documented contract and reuses
// every harbrr seam (paced HTTP client, secret store, normalized release, caps
// mapper, the /dl grab proxy, redaction).
package myanonamouse

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/loader"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/mapper"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
	"github.com/autobrr/harbrr/internal/indexer/native"
)

// driver is one configured MyAnonamouse instance. It is built once per instance and
// cached by the registry. The `mam_id` session cookie is held per driver and
// refreshed in-memory from each response's Set-Cookie (MAM rotates it on every
// request); the rotated value is also written back to the encrypted store (see the
// persist field) so the session survives a restart instead of reverting to the seed.
type driver struct {
	def     *loader.Definition
	caps    *mapper.Capabilities
	cfg     map[string]string
	doer    search.Doer
	baseURL string // normalised with a single trailing slash
	clock   func() time.Time
	log     zerolog.Logger

	// persist durably writes a rotated mam_id back to the encrypted store (nil in
	// tests / when the registry doesn't provide it). Fired best-effort on rotation so
	// the session survives a restart instead of reverting to the stored value.
	persist func(ctx context.Context, name, value string) error

	mu           sync.Mutex
	currentMamID string // rotating session cookie, seeded from cfg["mam_id"]
}

var _ native.Driver = (*driver)(nil)

// New is the native.Factory for MyAnonamouse. It builds the capabilities from the
// (Go-built) definition, normalises the base URL, and seeds the rotating mam_id from
// the decrypted settings.
func New(p native.Params) (native.Driver, error) {
	if p.Def == nil {
		return nil, errors.New("myanonamouse: nil definition")
	}
	caps, err := mapper.Build(p.Def)
	if err != nil {
		return nil, fmt.Errorf("myanonamouse: build capabilities for %q: %w", p.Def.ID, err)
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
		def:          p.Def,
		caps:         caps,
		cfg:          p.Cfg,
		doer:         p.Doer,
		baseURL:      strings.TrimRight(base, "/") + "/",
		clock:        clock,
		log:          p.Logger,
		persist:      p.PersistSetting,
		currentMamID: strings.TrimSpace(p.Cfg["mam_id"]),
	}, nil
}

// Capabilities returns the per-site capabilities document.
func (d *driver) Capabilities() *mapper.Capabilities { return d.caps }

// NeedsResolver is always true: a MyAnonamouse download URL must be fetched with the
// `mam_id` Cookie *arr cannot send, so the served feed routes through the /dl proxy
// and the driver's Grab fetches the torrent server-side.
func (d *driver) NeedsResolver() bool { return true }

// DownloadNeedsAuth is false: MAM is already routed through /dl by NeedsResolver, so
// the out-of-band-auth signal would be redundant.
func (d *driver) DownloadNeedsAuth() bool { return false }
