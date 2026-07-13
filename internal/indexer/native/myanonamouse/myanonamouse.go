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
	"strings"
	"sync"

	"github.com/autobrr/harbrr/internal/indexer/native"
)

// driver is one configured MyAnonamouse instance. It is built once per instance and
// cached by the registry. The `mam_id` session cookie is held per driver and
// refreshed in-memory from each response's Set-Cookie (MAM rotates it on every
// request); the rotated value is also written back to the encrypted store (see the
// persist field) so the session survives a restart instead of reverting to the seed.
// Everything but the MAM request/parse dialect and the rotation state lives in the
// embedded native.Base.
type driver struct {
	native.Base

	// persist durably writes a rotated mam_id back to the encrypted store (nil in
	// tests / when the registry doesn't provide it). Fired best-effort on rotation so
	// the session survives a restart instead of reverting to the stored value.
	persist func(ctx context.Context, name, value string) error

	mu           sync.Mutex
	currentMamID string // rotating session cookie, seeded from cfg["mam_id"]
}

var _ native.Driver = (*driver)(nil)

// New is the native.Factory for MyAnonamouse. It seeds the rotating mam_id from the
// decrypted settings.
func New(p native.Params) (native.Driver, error) {
	b, err := native.NewBase("myanonamouse", p)
	if err != nil {
		return nil, err
	}
	return &driver{
		Base:         b,
		persist:      p.PersistSetting,
		currentMamID: strings.TrimSpace(p.Cfg["mam_id"]),
	}, nil
}

// NeedsResolver is always true: a MyAnonamouse download URL must be fetched with the
// `mam_id` Cookie *arr cannot send, so the served feed routes through the /dl proxy
// and the driver's Grab fetches the torrent server-side.
func (d *driver) NeedsResolver() bool { return true }

// DownloadNeedsAuth is false: MAM is already routed through /dl by NeedsResolver, so
// the out-of-band-auth signal would be redundant.
func (d *driver) DownloadNeedsAuth() bool { return false }
