package torznab

import (
	"context"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
)

// Grab fetches the release's download URL server-side and returns the .torrent bytes.
// On the sealed sites (per-preset NeedsResolver — MoreThanTV's URLs carry
// authkey+torrent_pass in their query, which *arr must not see) this is the
// server-side fetch /dl drives, so the credentialed URL never reaches the feed; a
// bare-link site (AnimeTosho) serves its links directly and rarely routes here. The
// download is always a direct torrent, never a magnet, so GrabResult.Redirect stays
// empty. No error surfaces the download URL's secret path/query — Base's
// transport-error redaction (apphttp.SchemeHost + RedactURLError) already drops it
// before this function ever sees an error — and the bytes go to /dl, never a log.
// Mirrors filelist's and gazellegames' Grab exactly.
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
