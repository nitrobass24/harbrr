// Package iptorrents is the native driver for IPTorrents (IPT). IPTorrents has no
// Cardigann definition because its search surface is an HTML scrape whose column
// layout is resolved dynamically by header text and whose publish dates are
// relative "time ago" strings — logic the declarative YAML format cannot express.
// The driver reproduces Prowlarr's documented contract (IPTorrents.cs:
// IPTorrentsRequestGenerator / IPTorrentsParser) and reuses every harbrr seam
// (paced HTTP client, secret store, normalized release, caps mapper, the /dl grab
// proxy, redaction).
//
// Auth is a session cookie: the user pastes the full browser Cookie string plus a
// matching User-Agent, both sent as headers on every request. Because *arr cannot
// send that cookie, NeedsResolver is true and the served feed routes the download
// through /dl, where this driver's Grab fetches the .torrent server-side.
package iptorrents

import (
	"github.com/autobrr/harbrr/internal/indexer/native"
)

// driver is one configured IPTorrents instance. It is built once per instance and
// cached by the registry. There is no token to refresh — the cookie + User-Agent
// are static config, sent as headers on every request.
type driver struct {
	native.Base
}

var _ native.Driver = (*driver)(nil)

// New is the native.Factory for IPTorrents. It builds the capabilities from the
// Go-built definition and normalises the base URL.
func New(p native.Params) (native.Driver, error) {
	b, err := native.NewBase("iptorrents", p)
	if err != nil {
		return nil, err
	}
	return &driver{Base: b}, nil
}

// NeedsResolver is always true: an IPTorrents download must be fetched with the
// session cookie *arr cannot send, so the served feed routes through the /dl proxy
// and the driver's Grab fetches the torrent server-side.
func (d *driver) NeedsResolver() bool { return true }

// DownloadNeedsAuth is false: IPTorrents is already routed through /dl by
// NeedsResolver, so the out-of-band-auth signal would be redundant.
func (d *driver) DownloadNeedsAuth() bool { return false }
