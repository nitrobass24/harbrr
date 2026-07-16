package broadcastthenet

import (
	"context"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
	"github.com/autobrr/harbrr/internal/indexer/native"
)

// Grab fetches the BTN download URL server-side and returns the .torrent bytes. The
// URL embeds the authkey/torrent_pass in its query, which *arr must not see, which is
// why NeedsResolver is true and the served feed routes the download through the /dl
// proxy; this is the server-side fetch /dl drives, so the credential-bearing URL never
// reaches the feed. The download is a direct torrent (never a magnet), so Redirect is
// empty. No error carries the download link's secret path/query (its authkey/torrent_pass
// sit in the query); a transport error surfaces only its scheme://host, and the bytes go
// to /dl, never a log. GrabDirect (Base) owns the shared build-GET/DoDownload/GrabResult
// shape; ClassifyAuth403 is this family's dialect.
func (d *driver) Grab(ctx context.Context, link string) (*search.GrabResult, error) {
	return d.GrabDirect(ctx, link, native.ClassifyAuth403)
}
