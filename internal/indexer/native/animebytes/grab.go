package animebytes

import (
	"context"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
)

// Grab fetches the AnimeBytes download URL server-side and returns the .torrent bytes.
// The URL embeds the passkey (in its path, not only a query param), which *arr must not
// see, which is why NeedsResolver is true and the served feed routes the download
// through the /dl proxy; this is the server-side fetch /dl drives, so the
// passkey-bearing URL never reaches the feed. The download is a direct torrent (never a
// magnet), so Redirect is empty. No error carries the passkey — the passkey is in the
// path, which RedactURL (query-only) cannot strip, so native.Base surfaces only host-only
// details and never the passkey; the bytes go to /dl, never a log.
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
