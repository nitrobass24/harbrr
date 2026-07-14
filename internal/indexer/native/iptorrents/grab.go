package iptorrents

import (
	"context"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
)

// Grab fetches the resolved IPTorrents download URL with the session cookie + User-Agent
// and returns the .torrent bytes. *arr cannot send that cookie, which is why
// NeedsResolver is true and the served feed routes the download through /dl; this is the
// server-side fetch /dl drives, so neither the cookie nor the download URL reaches the
// feed. The download is a direct torrent (never a magnet), so Redirect is empty. No
// error carries the download link's secret path/query (only its scheme://host can), and
// the bytes go to /dl, never a log.
func (d *driver) Grab(ctx context.Context, link string) (*search.GrabResult, error) {
	resp, err := d.get(ctx, link, "", true)
	if err != nil {
		return nil, err
	}
	return &search.GrabResult{
		Body:        resp.Body,
		ContentType: resp.Header.Get("Content-Type"),
	}, nil
}
