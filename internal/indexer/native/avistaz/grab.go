package avistaz

import (
	"context"
	"errors"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
	"github.com/autobrr/harbrr/internal/indexer/native"
)

// errDownloadRequestFailed is the family-prefixed transport-failure sentinel handed to
// native.SanitizeGrabError, matching how newznab and nzbindex feed the same helper.
var errDownloadRequestFailed = errors.New("avistaz: download request failed")

// Grab fetches the resolved AvistaZ download URL with the Bearer header and returns
// the .torrent bytes. *arr cannot send the Bearer, which is why NeedsResolver is true
// and the served feed routes the download through the /dl proxy; this is the
// server-side fetch /dl drives, so neither the Bearer nor any key in the download URL
// reaches the feed. The download is a direct torrent (never a magnet), so Redirect is
// empty. A transport error surfaces only the scheme://host (native.SanitizeGrabError
// keeps the detail only when Base has PROVABLY scrubbed it host-only, and flattens
// anything else to the bare sentinel); the download URL's key — which may sit in its
// path, beyond the reach of the query-scoped URL redactor — never surfaces, and the
// bytes go to /dl, never a log.
func (d *driver) Grab(ctx context.Context, link string) (*search.GrabResult, error) {
	resp, err := d.get(ctx, link, "", true)
	if err != nil {
		if resp != nil {
			return nil, err
		}
		return nil, native.SanitizeGrabError(err, errDownloadRequestFailed)
	}
	return native.GrabResultFrom(resp), nil
}
