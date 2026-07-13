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

	"github.com/autobrr/harbrr/internal/indexer/native"
)

// driver is one configured HDBits instance. It is built once per instance and cached by
// the registry. There is no login round-trip: every request carries the username and
// passkey as top-level fields inside the JSON POST body, so the driver holds no session
// state. Everything but the HDBits request/parse dialect lives in the embedded
// native.Base.
type driver struct {
	native.Base
}

var _ native.Driver = (*driver)(nil)

// New is the native.Factory for HDBits.
func New(p native.Params) (native.Driver, error) {
	b, err := native.NewBase("hdbits", p)
	if err != nil {
		return nil, err
	}
	return &driver{Base: b}, nil
}

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
	return native.TestViaSearch(ctx, d)
}
