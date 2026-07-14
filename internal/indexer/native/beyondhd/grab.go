package beyondhd

import (
	"context"
	"errors"
	stdhttp "net/http"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
	"github.com/autobrr/harbrr/internal/indexer/native"
)

var errDownloadRequestFailed = errors.New("beyondhd: download request failed")

// Grab fetches the BeyondHD download_url server-side and returns the .torrent bytes. The
// URL embeds the rsskey in its PATH (torrent/download/auto.<id>.<rsskey>), which *arr must
// not see, which is why NeedsResolver is true and the served feed routes the download
// through the /dl proxy; this is the server-side fetch /dl drives, so the credential-bearing
// URL never reaches the feed. The download is a direct torrent (never a magnet), so Redirect
// is empty. No auth header is needed — the rsskey rides in the URL — so the GET is plain.
// No error carries the rsskey-bearing download URL: build errors are flattened, and
// transport errors surface only the request's scheme://host.
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
