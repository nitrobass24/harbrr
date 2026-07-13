package myanonamouse

import (
	"context"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
)

// Grab fetches the resolved download URL with the mam_id Cookie and returns the
// .torrent bytes. *arr cannot send the Cookie, which is why NeedsResolver is true and
// the served feed routes the download through the /dl proxy; this is the server-side
// fetch /dl drives, so neither the cookie nor any key in the download URL reaches the
// feed. The download is a direct torrent (never a magnet), so Redirect is empty.
// Status classification (MAM dialect), the torrent size cap
// (native.ErrDownloadTooLarge), and host-only transport redaction live in the base
// DoDownload: a grab error surfaces at most the download endpoint's scheme://host
// (never the mam_id, which rides a header, nor a secret in the URL's path/query), and
// the bytes go to /dl, never a log.
func (d *driver) Grab(ctx context.Context, link string) (*search.GrabResult, error) {
	req, err := d.newRequest(ctx, link, "")
	if err != nil {
		return nil, err
	}
	resp, err := d.doDownload(ctx, req)
	if err != nil {
		return nil, err
	}
	return &search.GrabResult{
		Body:        resp.Body,
		ContentType: resp.Header.Get("Content-Type"),
	}, nil
}
