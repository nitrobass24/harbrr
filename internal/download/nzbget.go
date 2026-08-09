package download

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/autobrr/harbrr/internal/domain"
	"github.com/autobrr/harbrr/internal/download/nzbget"
	apphttp "github.com/autobrr/harbrr/internal/http"
)

// nzbgetDriver wraps the ported nzbget.Client (autobrr's pkg/nzbget — JSON-RPC, plus
// harbrr's own base64-content append; see internal/download/nzbget's package doc).
type nzbgetDriver struct {
	client          *nzbget.Client
	defaultCategory string
}

// newNZBGet builds the NZBGet driver from a configured client row and its
// decrypted secret (the account password). Host column = base URL
// (e.g. http://host:6789); username + secret are HTTP Basic credentials.
func newNZBGet(c domain.DownloadClient, secret string, client *http.Client) (Driver, error) {
	var settings domain.NZBGetSettings
	if c.Settings.NZBGet != nil {
		settings = *c.Settings.NZBGet
	}
	return &nzbgetDriver{
		client: nzbget.New(nzbget.Options{
			Host:       c.Host,
			Username:   c.Username,
			Password:   secret,
			HTTPClient: client,
		}),
		defaultCategory: settings.Category,
	}, nil
}

// Test calls NZBGet's JSON-RPC "version" method, proving the host + credentials
// are reachable and valid.
func (d *nzbgetDriver) Test(ctx context.Context) error {
	if _, err := d.client.Version(ctx); err != nil {
		return fmt.Errorf("download: nzbget: version: %w", err)
	}
	return nil
}

// Add hands NZBGet an nzb via the JSON-RPC "append" method: the fetched bytes
// (base64-encoded content) when harbrr resolved them itself — a sealed harbrr download
// link is only fetchable by harbrr — otherwise a URL NZBGet fetches on its own.
// Priority/AddPaused/dupe fields are hardcoded upstream (0/false/SCORE); deliberately
// never sets a share-limit or auto-removal option.
func (d *nzbgetDriver) Add(ctx context.Context, p Payload, opts AddOptions) error {
	if p.Protocol != ProtocolUsenet {
		return fmt.Errorf("download: nzbget: %w: %s", ErrUnsupportedProtocol, p.Protocol)
	}

	category := opts.Category
	if category == "" {
		category = d.defaultCategory
	}

	if len(p.Bytes) > 0 {
		return d.appendContent(ctx, p, category)
	}
	if p.URL == "" {
		return fmt.Errorf("download: nzbget: %w", ErrURLRequired)
	}

	if _, err := d.client.AddFromURL(ctx, nzbget.AddNzbRequest{URL: p.URL, Category: category}); err != nil {
		// The nzb URL carries a harbrr API key (a sealed /dl link) and rides in the
		// RPC request body (not the request URL), but a transport failure still
		// surfaces as a *url.Error whose .URL is the jsonrpc endpoint — scrubURLError
		// drops it; the ReplaceAll is defense-in-depth for any literal-URL error
		// text, mirroring qbittorrent.go's Add treatment.
		err = scrubURLError(err)
		scrubbed := strings.ReplaceAll(err.Error(), p.URL, apphttp.RedactURL(p.URL))
		return fmt.Errorf("download: nzbget: add nzb from %s: %s", apphttp.RedactURL(p.URL), scrubbed)
	}
	return nil
}

// appendContent uploads the resolved .nzb bytes as base64 append content. No
// passkey-bearing link rides in this request, so only the *url.Error's own endpoint
// (which carries NZBGet's Basic credentials in neither URL nor body) is scrubbed, for
// the same defense-in-depth reason as the URL path.
func (d *nzbgetDriver) appendContent(ctx context.Context, p Payload, category string) error {
	_, err := d.client.AddFromContent(ctx, nzbget.AddNzbContentRequest{
		Filename: releaseFilename(p.Name, ".nzb"),
		Content:  p.Bytes,
		Category: category,
	})
	if err != nil {
		return fmt.Errorf("download: nzbget: upload nzb: %w", scrubURLError(err))
	}
	return nil
}
