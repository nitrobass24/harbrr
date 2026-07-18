package download

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/url"

	"github.com/hekmon/transmissionrpc/v3"

	"github.com/autobrr/harbrr/internal/domain"
)

// transmissionDriver wraps hekmon/transmissionrpc. The library owns the
// X-Transmission-Session-Id 409 handshake internally, so harbrr's shared
// *http.Client is thin enough to reuse directly (transmissionrpc.Config.CustomClient).
type transmissionDriver struct {
	client   *transmissionrpc.Client
	settings domain.TransmissionSettings
}

// newTransmission builds the Transmission driver. Host is the client's full RPC
// endpoint URL (e.g. http://host:9091/transmission/rpc — validated hostURL by
// service.validateHost); username/secret ride as URL userinfo, which net/http's
// Transport sends as Basic auth automatically. That URL is never logged, and
// never composed into an error string here: transmissionrpc's own errors don't
// echo the endpoint, and net/http's Client.Do has redacted URL passwords from
// its own wrapped errors since Go 1.15.
func newTransmission(c domain.DownloadClient, secret string, client *http.Client) (Driver, error) {
	endpoint, err := url.Parse(c.Host)
	if err != nil {
		return nil, fmt.Errorf("download: transmission: parse host: %w", err)
	}
	if c.Username != "" {
		if secret != "" {
			endpoint.User = url.UserPassword(c.Username, secret)
		} else {
			endpoint.User = url.User(c.Username)
		}
	}

	cli, err := transmissionrpc.New(endpoint, &transmissionrpc.Config{CustomClient: client})
	if err != nil {
		return nil, fmt.Errorf("download: transmission: %w", err)
	}

	var settings domain.TransmissionSettings
	if c.Settings.Transmission != nil {
		settings = *c.Settings.Transmission
	}
	return &transmissionDriver{client: cli, settings: settings}, nil
}

// Test confirms the library's RPC version satisfies the server's minimum,
// proving the endpoint and credentials are reachable and valid.
func (d *transmissionDriver) Test(ctx context.Context) error {
	ok, _, _, err := d.client.RPCVersion(ctx)
	if err != nil {
		return fmt.Errorf("download: transmission: rpc version: %w", err)
	}
	if !ok {
		return errors.New("download: transmission: unsupported rpc version")
	}
	return nil
}

// Add hands Transmission a torrent payload: a magnet/http(s) URL goes in
// Filename (Transmission fetches it itself), fetched bytes go in MetaInfo
// (base64). Labels carries the category + tags directly on the add call (RPC
// v16+) — no follow-up TorrentSet. TorrentAddPayload has no ratio/seed-time/
// removal field at all, so no-hit-and-run is enforced by the vendor type
// itself, not just by convention.
func (d *transmissionDriver) Add(ctx context.Context, p Payload, opts AddOptions) error {
	if p.Protocol != ProtocolTorrent {
		return fmt.Errorf("download: transmission: %w: %s", ErrUnsupportedProtocol, p.Protocol)
	}

	payload := transmissionrpc.TorrentAddPayload{Labels: transmissionLabels(opts)}
	if len(p.Bytes) > 0 {
		metainfo := base64.StdEncoding.EncodeToString(p.Bytes)
		payload.MetaInfo = &metainfo
	} else {
		payload.Filename = &p.URL
	}
	if d.settings.DownloadDir != "" {
		payload.DownloadDir = &d.settings.DownloadDir
	}
	if paused := opts.Paused || d.settings.StartPaused; paused {
		payload.Paused = &paused
	}

	if _, err := d.client.TorrentAdd(ctx, payload); err != nil {
		return fmt.Errorf("download: transmission: add torrent: %w", err)
	}
	return nil
}

// transmissionLabels builds the add payload's Labels list: the category first
// (if any), followed by tags — Transmission has no separate category concept.
func transmissionLabels(opts AddOptions) []string {
	var labels []string
	if opts.Category != "" {
		labels = append(labels, opts.Category)
	}
	return append(labels, opts.Tags...)
}
