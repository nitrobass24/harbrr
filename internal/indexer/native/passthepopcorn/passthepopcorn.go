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
	"github.com/autobrr/harbrr/internal/indexer/native"
)

// driver is one configured PassThePopcorn instance. It is built once per instance and
// cached by the registry. There is no login round-trip: every request carries the
// ApiUser/ApiKey credentials in request headers, so the driver holds no session state.
type driver struct {
	native.Base
}

var _ native.Driver = (*driver)(nil)

// New is the native.Factory for PassThePopcorn. It builds the capabilities from the
// definition and normalises the base URL.
func New(p native.Params) (native.Driver, error) {
	b, err := native.NewBase("passthepopcorn", p)
	if err != nil {
		return nil, err
	}
	return &driver{Base: b}, nil
}

// NeedsResolver is false: the download URL (torrents.php?action=download&id=...) carries
// no passkey, so the served feed link is safe to expose. The ApiUser/ApiKey headers are
// re-attached server-side at grab time, which DownloadNeedsAuth signals instead. (This
// matches the Gazelle model, not FileList/BroadcastTheNet — PTP's download URL embeds no
// secret; see PassThePopcornParser.GetDownloadUrl + PassThePopcorn.GetDownloadRequest.)
func (d *driver) NeedsResolver() bool { return false }

// DownloadNeedsAuth is true: the download authenticates out-of-band via the ApiUser/
// ApiKey headers, so the served feed routes through the /dl proxy and the driver's Grab
// (grab.go) fetches the torrent server-side with the headers attached.
func (d *driver) DownloadNeedsAuth() bool { return true }
