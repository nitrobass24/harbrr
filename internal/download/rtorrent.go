package download

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/autobrr/go-rtorrent"

	"github.com/autobrr/harbrr/internal/domain"
)

// errRTorrentFieldValue is returned when a label/directory value can't be sent
// as an rTorrent extra-arg: go-rtorrent's FieldValue.String() formats
// field.set="<value>" with no escaping, so a literal '"' would break out of
// the quoted argument and let the value inject additional rtorrent commands.
// Rejected rather than silently stripped — the value came from a release's
// category or a configured label/directory, and silently mangling it would be
// more surprising than a clear error.
var errRTorrentFieldValue = errors.New("download: rtorrent: field value must not contain a double quote")

// rtorrentDriver wraps autobrr/go-rtorrent (XML-RPC over HTTP(S)).
type rtorrentDriver struct {
	cli      *rtorrent.Client
	settings domain.RTorrentSettings
}

// newRTorrent builds the rTorrent driver. Host is the XML-RPC endpoint URL
// (hostURL, e.g. an httprpc/nginx mount).
//
// Deviation from the issue's literal `NewClient(cfg) + WithHTTPClient(client)`:
// (*rtorrent.Client).WithHTTPClient calls xmlrpc.NewClientWithHTTPClient(addr,
// client), which drops BasicUser/BasicPass entirely (verified against v1.12.0
// source) — a silent auth break for the exact case harbrr always hits (a
// Username+secret column). NewClientWithOpts(cfg, WithCustomClient(client))
// rebuilds the xmlrpc client from the full Config first, so BasicUser/BasicPass
// survive the client swap.
//
// TLSSkipVerify needs its own transport for the same underlying reason: passing
// ANY custom *http.Client to the library (via WithCustomClient or the
// WithHTTPClient method) always replaces its own TLS-aware transport
// (xmlrpc.NewClient builds a TLSSkipVerify-configured transport first, then
// unconditionally overwrites it if a Client is given). A dedicated client is
// built only when the setting is on — a shallow copy of the shared client with
// its Transport cloned and InsecureSkipVerify set, so Timeout and everything
// else about the shared client's configuration survives; otherwise harbrr's
// shared client is reused as-is, same as Transmission.
func newRTorrent(c domain.DownloadClient, secret string, client *http.Client) (Driver, error) {
	var settings domain.RTorrentSettings
	if c.Settings.RTorrent != nil {
		settings = *c.Settings.RTorrent
	}

	httpClient := client
	if settings.TLSSkipVerify {
		clone := *client // shallow copy: keeps Timeout, CheckRedirect, Jar, etc.
		transport, ok := client.Transport.(*http.Transport)
		if !ok {
			transport = http.DefaultTransport.(*http.Transport) //nolint:forcetypeassert // http.DefaultTransport is always *http.Transport.
		}
		transport = transport.Clone()
		if transport.TLSClientConfig == nil {
			transport.TLSClientConfig = &tls.Config{}
		}
		transport.TLSClientConfig.InsecureSkipVerify = true //nolint:gosec // opt-in per client, mirrors qBittorrent's TLSSkipVerify.
		clone.Transport = transport
		httpClient = &clone
	}

	cfg := rtorrent.Config{Addr: c.Host, TLSSkipVerify: settings.TLSSkipVerify, BasicUser: c.Username, BasicPass: secret}
	cli := rtorrent.NewClientWithOpts(cfg, rtorrent.WithCustomClient(httpClient))
	return &rtorrentDriver{cli: cli, settings: settings}, nil
}

// Test confirms the endpoint + basic-auth credentials are reachable and valid.
func (d *rtorrentDriver) Test(ctx context.Context) error {
	if _, err := d.cli.Name(ctx); err != nil {
		return fmt.Errorf("download: rtorrent: name: %w", err)
	}
	return nil
}

// Add hands rTorrent a torrent payload: a magnet or http(s) URL goes through
// Add/AddStopped (rTorrent fetches it itself); fetched bytes go through
// AddTorrent/AddTorrentStopped. Paused uses the Stopped variant. Label and
// directory ride as extra d.custom1/d.directory field-set args on the same
// call — no ratio/seed-time/removal field exists to set.
func (d *rtorrentDriver) Add(ctx context.Context, p Payload, opts AddOptions) error {
	if p.Protocol != ProtocolTorrent {
		return fmt.Errorf("download: rtorrent: %w: %s", ErrUnsupportedProtocol, p.Protocol)
	}

	args, err := d.fieldArgs(opts)
	if err != nil {
		return err
	}
	paused := opts.Paused || d.settings.StartPaused

	switch {
	case len(p.Bytes) > 0 && paused:
		err = d.cli.AddTorrentStopped(ctx, p.Bytes, args...)
	case len(p.Bytes) > 0:
		err = d.cli.AddTorrent(ctx, p.Bytes, args...)
	case paused:
		err = d.cli.AddStopped(ctx, p.URL, args...)
	default:
		err = d.cli.Add(ctx, p.URL, args...)
	}
	if err != nil {
		return fmt.Errorf("download: rtorrent: add torrent: %w", err)
	}
	return nil
}

// fieldArgs builds the d.custom1 (label) / d.directory extra args for the add
// call: label falls back to settings when no category is given; directory is
// settings-only (harbrr has no per-add directory option). Only the values that
// will actually be emitted are checked for an injection-capable '"' — a stored
// label a per-add category overrides is never sent, so it can't reject the add.
func (d *rtorrentDriver) fieldArgs(opts AddOptions) ([]*rtorrent.FieldValue, error) {
	label := opts.Category
	if label == "" {
		label = d.settings.Label
	}
	for _, v := range []string{label, d.settings.Directory} {
		if strings.Contains(v, `"`) {
			return nil, fmt.Errorf("%w: %q", errRTorrentFieldValue, v)
		}
	}

	var args []*rtorrent.FieldValue
	if label != "" {
		args = append(args, rtorrent.DLabel.SetValue(label))
	}
	if d.settings.Directory != "" {
		args = append(args, rtorrent.DDirectory.SetValue(d.settings.Directory))
	}
	return args, nil
}
