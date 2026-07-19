package download

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"

	"github.com/autobrr/go-deluge"

	"github.com/autobrr/harbrr/internal/domain"
)

// errDelugeLabelPluginDisabled is returned by delugeClient's label methods when
// the remote daemon doesn't have the Label plugin enabled. The driver treats it
// as "nothing to label" rather than a failure, mirroring autobrr's
// internal/action/deluge.go.
var errDelugeLabelPluginDisabled = errors.New("download: deluge: label plugin not enabled")

// delugeClient is the exact subset of the vendored go-deluge client's RPC
// surface delugeDriver needs. Deluge speaks rencode over a raw TLS socket (no
// HTTP), so unlike Transmission/rTorrent it can't be pointed at an httptest
// server — this interface exists purely so a fake can stand in for tests. It
// runs past harbrr's usual ≤5-method interface guideline as a deliberate,
// approved exception: it's a 1:1 mirror of an unfakeable vendor socket surface,
// not a speculative abstraction. SetTorrentLabel/AddLabel stay separate
// primitives (rather than one "set label" call) so the create-on-miss retry
// dance lives in delugeDriver, where a fake can exercise and assert it.
type delugeClient interface {
	Connect(ctx context.Context) error
	Close() error
	DaemonVersion(ctx context.Context) (string, error)
	AddTorrentMagnet(ctx context.Context, uri string, opts *deluge.Options) (string, error)
	AddTorrentURL(ctx context.Context, url string, opts *deluge.Options) (string, error)
	AddTorrentFile(ctx context.Context, name, contentBase64 string, opts *deluge.Options) (string, error)
	SetTorrentLabel(ctx context.Context, hash, label string) error
	AddLabel(ctx context.Context, label string) error
}

// delugeVendorClient is the shape both deluge.NewV1's *deluge.Client and
// deluge.NewV2's *deluge.ClientV2 satisfy structurally (ClientV2 embeds Client,
// promoting LabelPlugin and friends) — realDelugeClient's construction target.
// LabelPlugin isn't part of the vendor's own exported DelugeClient interface,
// only on the concrete types, which is why this interface exists separately.
type delugeVendorClient interface {
	Connect(ctx context.Context) error
	Close() error
	DaemonVersion(ctx context.Context) (string, error)
	AddTorrentMagnet(ctx context.Context, uri string, opts *deluge.Options) (string, error)
	AddTorrentURL(ctx context.Context, url string, opts *deluge.Options) (string, error)
	AddTorrentFile(ctx context.Context, name, contentBase64 string, opts *deluge.Options) (string, error)
	LabelPlugin(ctx context.Context) (*deluge.LabelPlugin, error)
}

// realDelugeClient adapts a delugeVendorClient to delugeClient, resolving the
// Label plugin on demand for each label op — it's a rare path, so there's no
// need to cache the plugin handle across calls.
type realDelugeClient struct {
	vendor delugeVendorClient
}

func (r *realDelugeClient) Connect(ctx context.Context) error { return r.vendor.Connect(ctx) }
func (r *realDelugeClient) Close() error                      { return r.vendor.Close() }

func (r *realDelugeClient) DaemonVersion(ctx context.Context) (string, error) {
	return r.vendor.DaemonVersion(ctx)
}

func (r *realDelugeClient) AddTorrentMagnet(ctx context.Context, uri string, opts *deluge.Options) (string, error) {
	return r.vendor.AddTorrentMagnet(ctx, uri, opts)
}

func (r *realDelugeClient) AddTorrentURL(ctx context.Context, url string, opts *deluge.Options) (string, error) {
	return r.vendor.AddTorrentURL(ctx, url, opts)
}

func (r *realDelugeClient) AddTorrentFile(ctx context.Context, name, contentBase64 string, opts *deluge.Options) (string, error) {
	return r.vendor.AddTorrentFile(ctx, name, contentBase64, opts)
}

func (r *realDelugeClient) SetTorrentLabel(ctx context.Context, hash, label string) error {
	plugin, err := r.vendor.LabelPlugin(ctx)
	if err != nil {
		return err
	}
	if plugin == nil {
		return errDelugeLabelPluginDisabled
	}
	if err := plugin.SetTorrentLabel(ctx, hash, label); err != nil {
		return fmt.Errorf("set torrent label: %w", err)
	}
	return nil
}

func (r *realDelugeClient) AddLabel(ctx context.Context, label string) error {
	plugin, err := r.vendor.LabelPlugin(ctx)
	if err != nil {
		return err
	}
	if plugin == nil {
		return errDelugeLabelPluginDisabled
	}
	if err := plugin.AddLabel(ctx, label); err != nil {
		return fmt.Errorf("add label: %w", err)
	}
	return nil
}

// delugeDriver wraps a Deluge daemon connection. Every RPC needs Connect
// first — Test and Add each connect and defer Close rather than holding a
// long-lived session, keeping the driver stateless between calls like the
// other two RPC drivers.
type delugeDriver struct {
	cli      delugeClient
	settings domain.DelugeSettings
}

// newDeluge builds the Deluge driver. Host is validated hostPort by
// service.validateHost ("daemonhost:58846"), so SplitHostPort here cannot fail
// on a client actually reachable through Create/Update.
func newDeluge(c domain.DownloadClient, secret string, _ *http.Client) (Driver, error) {
	host, portStr, err := net.SplitHostPort(c.Host)
	if err != nil {
		return nil, fmt.Errorf("download: deluge: parse host: %w", err)
	}
	port, err := strconv.ParseUint(portStr, 10, 16)
	if err != nil {
		return nil, fmt.Errorf("download: deluge: parse port: %w", err)
	}

	var settings domain.DelugeSettings
	if c.Settings.Deluge != nil {
		settings = *c.Settings.Deluge
	}

	rpcSettings := deluge.Settings{Hostname: host, Port: uint(port), Login: c.Username, Password: secret}
	var vendor delugeVendorClient
	if settings.V1 {
		vendor = deluge.NewV1(rpcSettings)
	} else {
		vendor = deluge.NewV2(rpcSettings)
	}

	return &delugeDriver{cli: &realDelugeClient{vendor: vendor}, settings: settings}, nil
}

// Test connects and reads the daemon version, proving the host + credentials work.
func (d *delugeDriver) Test(ctx context.Context) error {
	if err := d.cli.Connect(ctx); err != nil {
		return fmt.Errorf("download: deluge: connect: %w", err)
	}
	defer d.cli.Close()
	if _, err := d.cli.DaemonVersion(ctx); err != nil {
		return fmt.Errorf("download: deluge: daemon version: %w", err)
	}
	return nil
}

// Add hands Deluge a torrent payload and, if a category/label is set, applies
// it. Only AddPaused and DownloadLocation ride on the add call — Options also
// carries StopAtRatio/RemoveAtRatio, which this driver never sets
// (no-hit-and-run).
func (d *delugeDriver) Add(ctx context.Context, p Payload, opts AddOptions) error {
	if p.Protocol != ProtocolTorrent {
		return fmt.Errorf("download: deluge: %w: %s", ErrUnsupportedProtocol, p.Protocol)
	}
	if err := d.cli.Connect(ctx); err != nil {
		return fmt.Errorf("download: deluge: connect: %w", err)
	}
	defer d.cli.Close()

	hash, err := d.addTorrent(ctx, p, d.addOptions(opts))
	if err != nil {
		return fmt.Errorf("download: deluge: add torrent: %w", err)
	}

	label := opts.Category
	if label == "" {
		label = d.settings.Label
	}
	if label == "" {
		return nil
	}
	if err := d.setLabel(ctx, hash, label); err != nil {
		return fmt.Errorf("download: deluge: set label: %w", err)
	}
	return nil
}

// addOptions builds the add-torrent Options: AddPaused from opts/settings,
// DownloadLocation from settings.
func (d *delugeDriver) addOptions(opts AddOptions) *deluge.Options {
	rpcOpts := &deluge.Options{}
	if paused := opts.Paused || d.settings.StartPaused; paused {
		rpcOpts.AddPaused = &paused
	}
	if d.settings.DownloadDir != "" {
		dir := d.settings.DownloadDir
		rpcOpts.DownloadLocation = &dir
	}
	return rpcOpts
}

// addTorrent dispatches to the add call matching the payload's form: a magnet
// URI, an http(s) URL Deluge fetches itself, or fetched bytes (base64-encoded).
func (d *delugeDriver) addTorrent(ctx context.Context, p Payload, rpcOpts *deluge.Options) (string, error) {
	switch {
	case len(p.Bytes) > 0:
		name := p.Name
		if name == "" {
			name = "harbrr.torrent"
		}
		return d.cli.AddTorrentFile(ctx, name, base64.StdEncoding.EncodeToString(p.Bytes), rpcOpts)
	case strings.HasPrefix(p.URL, "magnet:"):
		return d.cli.AddTorrentMagnet(ctx, p.URL, rpcOpts)
	default:
		return d.cli.AddTorrentURL(ctx, p.URL, rpcOpts)
	}
}

// setLabel applies label to hash, creating the label definition on the daemon
// first if Deluge doesn't know it yet (a fresh label must be declared via
// AddLabel before a torrent can be tagged with it) and retrying once — the
// same create-on-miss dance autobrr's action/deluge.go uses. A daemon with the
// Label plugin disabled is not an error; there's simply nothing to label.
func (d *delugeDriver) setLabel(ctx context.Context, hash, label string) error {
	err := d.cli.SetTorrentLabel(ctx, hash, label)
	switch {
	case err == nil, errors.Is(err, errDelugeLabelPluginDisabled):
		return nil
	case isUnknownLabel(err):
		if err := d.cli.AddLabel(ctx, label); err != nil {
			return err
		}
		return d.cli.SetTorrentLabel(ctx, hash, label)
	default:
		return err
	}
}

// isUnknownLabel reports whether err is Deluge's RPC error for a label the
// daemon hasn't seen before.
func isUnknownLabel(err error) bool {
	var rpcErr deluge.RPCError
	return errors.As(err, &rpcErr) && rpcErr.ExceptionMessage == "Unknown Label"
}
