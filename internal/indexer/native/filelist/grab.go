package filelist

import (
	"context"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
)

// Grab fetches the rebuilt download.php URL with the Basic header and returns the
// .torrent bytes. The download URL carries the passkey in its query, which *arr must
// not see, which is why NeedsResolver is true and the served feed routes the download
// through the /dl proxy; this is the server-side fetch /dl drives, so neither the
// Basic header nor the passkey in the URL reaches the feed. The download is a direct
// torrent (never a magnet), so Redirect is empty. On a fetch failure the error
// surfaces only the download URL's scheme://host, and the bytes go to /dl, never a log.
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
