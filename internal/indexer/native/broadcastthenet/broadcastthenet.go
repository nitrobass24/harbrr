// Package broadcastthenet is the native driver for BroadcastTheNet
// (api.broadcasthe.net). It has no Cardigann definition because its JSON-RPC 2.0
// getTorrents endpoint — an API key carried as the first positional param inside the
// request body, a torrents map keyed by id with every field wire-encoded as a string,
// and a download URL that embeds the authkey/torrent_pass — exceeds the declarative
// format, so the search/parse/grab logic lives here in Go. The driver reproduces
// Prowlarr's documented contract (BroadcastheNetRequestGenerator /
// BroadcastheNetParser / BroadcastheNetSettings) and reuses every harbrr seam (paced
// HTTP client, secret store, normalized release, caps mapper, the /dl grab proxy,
// redaction).
package broadcastthenet

import (
	"context"

	"github.com/autobrr/harbrr/internal/indexer/native"
)

// driver is one configured BroadcastTheNet instance. It is built once per instance and
// cached by the registry. There is no login round-trip: every request carries the API
// key as the first positional param inside the JSON-RPC body, so the driver holds no
// session state. Everything but the BTN request/parse dialect lives in the embedded
// native.Base.
type driver struct {
	native.Base
}

var _ native.Driver = (*driver)(nil)

// New is the native.Factory for BroadcastTheNet. It builds the capabilities from the
// definition and normalises the base URL.
func New(p native.Params) (native.Driver, error) {
	b, err := native.NewBase("broadcastthenet", p)
	if err != nil {
		return nil, err
	}
	return &driver{Base: b}, nil
}

// NeedsResolver is always true: a BroadcastTheNet download URL embeds the authkey and
// torrent_pass in its query, which *arr must not see, so the served feed routes through
// the /dl proxy and the driver's Grab fetches the torrent server-side.
func (d *driver) NeedsResolver() bool { return true }

// DownloadNeedsAuth is false: the download URL already carries its own credentials and
// is routed through /dl by NeedsResolver, so the out-of-band-auth signal would be
// redundant (it mirrors FileList/AvistaZ).
func (d *driver) DownloadNeedsAuth() bool { return false }

// Test verifies the configured API key authenticates (the management "test indexer"
// action) by issuing an empty browse query: a good key returns 200 with a result
// envelope, a bad key surfaces as login.ErrLoginFailed (HTTP 401/403 or the -32001
// JSON-RPC error).
func (d *driver) Test(ctx context.Context) error {
	return native.TestViaSearch(ctx, d)
}
