package download

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/autobrr/harbrr/internal/domain"
)

// Protocol distinguishes a payload's release type: a client that only speaks one
// protocol (e.g. an nzb-only client) rejects the other with
// ErrUnsupportedProtocol rather than silently mishandling it.
type Protocol string

const (
	ProtocolTorrent Protocol = "torrent"
	ProtocolUsenet  Protocol = "usenet"
)

// Payload is the resolved release handed to a Driver's Add. Bytes is set when
// harbrr fetched the .torrent/.nzb itself (e.g. behind a resolver); otherwise URL
// carries a link the client fetches on its own — a magnet URI, a sealed harbrr
// /dl link, or an nzb URL. Exactly one of Bytes/URL is populated.
type Payload struct {
	Protocol Protocol
	URL      string
	Bytes    []byte
	Name     string
}

// AddOptions are the caller's (harbrr's) intent for a newly-added release. It
// deliberately excludes any share-limit or auto-removal fields — harbrr never
// hit-and-runs a client-managed torrent (see #240's driver tests).
type AddOptions struct {
	Category string
	Tags     []string
	Paused   bool
}

// ErrUnsupportedProtocol is returned by a Driver whose client cannot handle the
// given Payload's Protocol (e.g. a torrent-only client sent a usenet payload).
var ErrUnsupportedProtocol = errors.New("download: client does not support payload protocol")

// Driver is the minimal interface a download-client kind implements. Test proves
// the configured client is reachable with its stored credentials; Add hands it a
// resolved release to start downloading.
type Driver interface {
	Test(ctx context.Context) error
	Add(ctx context.Context, p Payload, opts AddOptions) error
}

// driverBuilder constructs a Driver for one configured client. secret is the
// already-decrypted credential (password/API key, meaning depends on kind);
// client is a shared *http.Client for drivers thin enough to use one directly
// (a driver that owns its own client, like qBittorrent's session-cookie client,
// ignores it).
type driverBuilder func(c domain.DownloadClient, secret string, client *http.Client) (Driver, error)

// hostMode is the shape a kind's host column must take, since not every
// registered client is reachable by URL: Deluge's daemon RPC is a raw socket
// address ("host:port"), not a URL at all.
type hostMode int

const (
	hostURL  hostMode = iota // absolute http(s) URL (the default)
	hostPort                 // "host:port" via net.SplitHostPort
)

// driverSpec pairs a kind's driver constructor with its host column's shape.
type driverSpec struct {
	build driverBuilder
	host  hostMode
}

// drivers is the single source of truth for both construction AND kind validity:
// a kind is creatable only once it has an entry here. Adding a driver is one map
// entry (plus its own file) — no other platform code changes.
var drivers = map[string]driverSpec{
	domain.DownloadClientKindQBittorrent:  {build: newQBittorrent, host: hostURL},
	domain.DownloadClientKindTransmission: {build: newTransmission, host: hostURL},
	domain.DownloadClientKindRTorrent:     {build: newRTorrent, host: hostURL},
	domain.DownloadClientKindDeluge:       {build: newDeluge, host: hostPort},
}

// validateKind reports whether kind has a registered driver.
func validateKind(kind string) bool {
	_, ok := drivers[kind]
	return ok
}

// newDriver builds the Driver for a configured client, or an error if its kind
// has no registered driver.
func newDriver(c domain.DownloadClient, secret string, client *http.Client) (Driver, error) {
	spec, ok := drivers[c.Kind]
	if !ok {
		return nil, fmt.Errorf("%w: unregistered download client kind %q", domain.ErrInvalid, c.Kind)
	}
	return spec.build(c, secret, client)
}
