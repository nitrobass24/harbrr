package beyondhd

import (
	"context"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
	"github.com/autobrr/harbrr/internal/indexer/native"
)

// Grab fetches the BeyondHD download_url server-side and returns the .torrent bytes. The
// URL embeds the rsskey in its PATH (torrent/download/auto.<id>.<rsskey>), which *arr must
// not see, which is why NeedsResolver is true and the served feed routes the download
// through the /dl proxy; this is the server-side fetch /dl drives, so the credential-bearing
// URL never reaches the feed. The download is a direct torrent (never a magnet), so Redirect
// is empty. No auth header is needed — the rsskey rides in the URL — so the GET is plain.
// No error carries the rsskey-bearing download URL: build errors are flattened, and
// transport errors surface only the request's scheme://host. GrabDirect (Base) owns the
// shared build-GET/DoDownload/GrabResult shape; ClassifyAuth403 is this family's dialect.
func (d *driver) Grab(ctx context.Context, link string) (*search.GrabResult, error) {
	return d.GrabDirect(ctx, link, native.ClassifyAuth403)
}
