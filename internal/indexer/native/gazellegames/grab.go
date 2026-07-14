package gazellegames

import (
	"context"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
)

// Grab fetches the rebuilt torrents.php download URL server-side and returns the
// .torrent bytes. The URL carries the passkey in its torrent_pass query, which *arr must
// not see, which is why NeedsResolver is true and the served feed routes the download
// through the /dl proxy; this is the server-side fetch /dl drives, so neither the
// X-API-Key header nor the passkey in the URL reaches the feed. The download is a direct
// torrent (never a magnet), so Redirect is empty. No error surfaces the download URL's
// secret path/query (its passkey sits in the query) — only its scheme://host can — and the
// bytes go to /dl, never a log.
func (d *driver) Grab(ctx context.Context, link string) (*search.GrabResult, error) {
	resp, err := d.get(ctx, link, true)
	if err != nil {
		return nil, err
	}
	return &search.GrabResult{
		Body:        resp.Body,
		ContentType: resp.Header.Get("Content-Type"),
	}, nil
}
