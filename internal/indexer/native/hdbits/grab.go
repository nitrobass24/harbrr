package hdbits

import (
	"context"
	"errors"
	stdhttp "net/http"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
	"github.com/autobrr/harbrr/internal/indexer/native"
)

// errDownloadRequestFailed is the grab-path build-request failure. A request that
// cannot even be built may quote the passkey-bearing download URL in its cause, so it
// is returned bare — never wrapped around the underlying error.
var errDownloadRequestFailed = errors.New("hdbits: download request failed")

// Grab fetches the rebuilt download.php URL server-side and returns the .torrent bytes.
// The download URL embeds the passkey in its query (download.php?id=…&passkey=…), which
// *arr must not see, which is why NeedsResolver is true and the served feed routes the
// download through the /dl proxy; this is the server-side fetch /dl drives, so the
// passkey-bearing URL never reaches the feed. The URL already carries its own passkey,
// so no auth header is set. The download is a direct torrent (never a magnet), so
// Redirect is empty. Transport redaction and the 403-is-rate-limit classification
// (mirroring Search) live in the base DoDownload: a grab error surfaces at most the
// download endpoint's scheme://host — never the passkey — and the bytes go to /dl,
// never a log.
func (d *driver) Grab(ctx context.Context, link string) (*search.GrabResult, error) {
	req, err := stdhttp.NewRequestWithContext(ctx, stdhttp.MethodGet, link, nil)
	if err != nil {
		return nil, errDownloadRequestFailed
	}
	resp, err := d.DoDownload(ctx, req, native.ClassifyRateLimit403)
	if err != nil {
		return nil, err
	}
	return &search.GrabResult{
		Body:        resp.Body,
		ContentType: resp.Header.Get("Content-Type"),
	}, nil
}
