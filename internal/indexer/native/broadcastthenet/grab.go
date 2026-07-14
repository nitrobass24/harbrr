package broadcastthenet

import (
	"context"
	"errors"
	stdhttp "net/http"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
	"github.com/autobrr/harbrr/internal/indexer/native"
)

var errDownloadRequestFailed = errors.New("broadcastthenet: download request failed")

// Grab fetches the BTN download URL server-side and returns the .torrent bytes. The
// URL embeds the authkey/torrent_pass in its query, which *arr must not see, which is
// why NeedsResolver is true and the served feed routes the download through the /dl
// proxy; this is the server-side fetch /dl drives, so the credential-bearing URL never
// reaches the feed. The download is a direct torrent (never a magnet), so Redirect is
// empty. No error carries the download link's secret path/query (its authkey/torrent_pass
// sit in the query); a transport error surfaces only its scheme://host, and the bytes go
// to /dl, never a log.
func (d *driver) Grab(ctx context.Context, link string) (*search.GrabResult, error) {
	req, err := stdhttp.NewRequestWithContext(ctx, stdhttp.MethodGet, link, nil)
	if err != nil {
		return nil, errDownloadRequestFailed
	}
	resp, err := d.DoDownload(ctx, req, native.ClassifyAuth403)
	if err != nil {
		return nil, err
	}
	return &search.GrabResult{
		Body:        resp.Body,
		ContentType: resp.Header.Get("Content-Type"),
	}, nil
}
