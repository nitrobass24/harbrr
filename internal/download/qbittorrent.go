package download

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/autobrr/go-qbittorrent"

	"github.com/autobrr/harbrr/internal/domain"
	apphttp "github.com/autobrr/harbrr/internal/http"
)

// errAddReportedFailure is returned when qBittorrent's add response reports every
// torrent failed (no successes) without a transport-level error.
var errAddReportedFailure = errors.New("download: qbittorrent: add torrent: qBittorrent reported failure")

// qbittorrentDriver wraps the go-qbittorrent client. The lib owns its own HTTP
// client and cookie jar (it authenticates with a session cookie, not a bearer
// token), so the factory's shared *http.Client goes unused here — it exists for
// #242's thin HTTP drivers, not this one.
type qbittorrentDriver struct {
	client   *qbittorrent.Client
	category string
	tags     []string
	paused   bool
}

// newQBittorrent builds the qBittorrent driver from a configured client row and
// its decrypted secret (the account password; empty for the credential-free
// localhost-bypass case). The per-client category/tags/start-paused settings are held
// here and folded in by Add, the way every other driver folds its own defaults.
func newQBittorrent(c domain.DownloadClient, secret string, _ *http.Client) (Driver, error) {
	var settings domain.QBittorrentSettings
	if c.Settings.QBittorrent != nil {
		settings = *c.Settings.QBittorrent
	}
	return &qbittorrentDriver{
		client: qbittorrent.NewClient(qbittorrent.Config{
			Host:          c.Host,
			Username:      c.Username,
			Password:      secret,
			TLSSkipVerify: settings.TLSSkipVerify,
		}),
		category: settings.Category,
		tags:     settings.Tags,
		paused:   settings.StartPaused,
	}, nil
}

// Test logs in, proving the configured host + credentials are reachable and
// valid.
func (d *qbittorrentDriver) Test(ctx context.Context) error {
	if err := d.client.LoginCtx(ctx); err != nil {
		return fmt.Errorf("download: qbittorrent: login: %w", err)
	}
	return nil
}

// Add hands qBittorrent a torrent payload, either fetched bytes (multipart
// upload) or a URL it fetches itself (magnet, a sealed harbrr /dl link, or a
// plain http(s) .torrent link). Deliberately never sets a share-limit or
// auto-removal option — harbrr does not hit-and-run a client-managed torrent.
func (d *qbittorrentDriver) Add(ctx context.Context, p Payload, opts AddOptions) error {
	if p.Protocol != ProtocolTorrent {
		return fmt.Errorf("download: qbittorrent: %w: %s", ErrUnsupportedProtocol, p.Protocol)
	}
	if err := d.client.LoginCtx(ctx); err != nil {
		return fmt.Errorf("download: qbittorrent: login: %w", err)
	}

	category := d.category
	if opts.Category != "" {
		category = opts.Category
	}
	form := (&qbittorrent.TorrentAddOptions{
		Paused:   d.paused || opts.Paused,
		Category: category,
		Tags:     strings.Join(mergeTags(d.tags, opts.Tags), ","),
	}).Prepare()

	var (
		resp *qbittorrent.TorrentAddResponse
		err  error
	)
	if len(p.Bytes) > 0 {
		resp, err = d.client.AddTorrentFromMemoryCtx(ctx, p.Bytes, form)
		if err != nil {
			return fmt.Errorf("download: qbittorrent: add torrent: %w", err)
		}
	} else {
		resp, err = d.client.AddTorrentFromUrlCtx(ctx, p.URL, form)
		if err != nil {
			// go-qbittorrent embeds the submitted URL in its add errors, and a
			// sealed harbrr /dl link carries the apikey — scrub every occurrence
			// before surfacing so it can't reach a log.
			scrubbed := strings.ReplaceAll(err.Error(), p.URL, apphttp.RedactURL(p.URL))
			return fmt.Errorf("download: qbittorrent: add torrent from %s: %s", apphttp.RedactURL(p.URL), scrubbed)
		}
	}
	if resp.FailureCount > 0 && resp.SuccessCount == 0 {
		return errAddReportedFailure
	}
	return nil
}
