package download

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/autobrr/harbrr/internal/domain"
	"github.com/autobrr/harbrr/internal/download/sabnzbd"
	apphttp "github.com/autobrr/harbrr/internal/http"
)

// sabnzbdDriver wraps the ported sabnzbd.Client (autobrr's pkg/sabnzbd — add-by-URL
// only, no bytes upload; see internal/download/sabnzbd's package doc).
type sabnzbdDriver struct {
	client          *sabnzbd.Client
	defaultCategory string
}

// newSabnzbd builds the SABnzbd driver from a configured client row and its
// decrypted secret (the SABnzbd API key). Host column = base URL; username is
// unused (not exposed by harbrr — SABnzbd's optional HTTP Basic auth can be
// added on real demand).
func newSabnzbd(c domain.DownloadClient, secret string, client *http.Client) (Driver, error) {
	var settings domain.SabnzbdSettings
	if c.Settings.Sabnzbd != nil {
		settings = *c.Settings.Sabnzbd
	}
	return &sabnzbdDriver{
		client: sabnzbd.New(sabnzbd.Options{
			Addr:       c.Host,
			ApiKey:     secret,
			HTTPClient: client,
		}),
		defaultCategory: settings.Category,
	}, nil
}

// Test calls SABnzbd's version endpoint, proving the host + apikey are
// reachable. VersionResponse (unlike AddFileResponse) carries no ApiError field
// upstream, so a rejected apikey is only distinguishable here if SABnzbd answers
// with a body Version can't decode (its ported client never checks HTTP status).
func (d *sabnzbdDriver) Test(ctx context.Context) error {
	if _, err := d.client.Version(ctx); err != nil {
		return fmt.Errorf("download: sabnzbd: version: %w", err)
	}
	return nil
}

// Add hands SABnzbd an nzb by URL — SABnzbd fetches it itself; the ported client
// has no bytes-upload path. Deliberately never sets a share-limit or
// auto-removal option (harbrr does not hit-and-run a client-managed download).
func (d *sabnzbdDriver) Add(ctx context.Context, p Payload, opts AddOptions) error {
	if p.Protocol != ProtocolUsenet {
		return fmt.Errorf("download: sabnzbd: %w: %s", ErrUnsupportedProtocol, p.Protocol)
	}
	if p.URL == "" {
		return fmt.Errorf("download: sabnzbd: %w", ErrURLRequired)
	}

	category := opts.Category
	if category == "" {
		category = d.defaultCategory
	}

	resp, err := d.client.AddFromUrl(ctx, sabnzbd.AddNzbRequest{Url: p.URL, Category: category})
	if err != nil {
		// The nzb URL carries a harbrr API key (a sealed /dl link) and is embedded
		// in SABnzbd's own request (as the "name" query param). A transport
		// failure surfaces as a *url.Error whose .URL is the (percent-encoded)
		// request URL — scrubURLError drops it entirely; the ReplaceAll is
		// defense-in-depth for any literal-URL error text, mirroring
		// qbittorrent.go's Add treatment.
		err = scrubURLError(err)
		scrubbed := strings.ReplaceAll(err.Error(), p.URL, apphttp.RedactURL(p.URL))
		return fmt.Errorf("download: sabnzbd: add nzb from %s: %s", apphttp.RedactURL(p.URL), scrubbed)
	}
	if resp.ErrorMsg != "" {
		return fmt.Errorf("download: sabnzbd: add nzb: %s", resp.ErrorMsg)
	}
	return nil
}
