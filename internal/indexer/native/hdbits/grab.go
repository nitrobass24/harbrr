package hdbits

import (
	"context"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
	"github.com/autobrr/harbrr/internal/indexer/native"
)

// Grab fetches the rebuilt download.php URL server-side and returns the .torrent bytes.
// The download URL embeds the passkey in its query (download.php?id=…&passkey=…), which
// *arr must not see, which is why NeedsResolver is true and the served feed routes the
// download through the /dl proxy; this is the server-side fetch /dl drives, so the
// passkey-bearing URL never reaches the feed. The URL already carries its own passkey,
// so no auth header is set. The download is a direct torrent (never a magnet), so
// Redirect is empty. Transport redaction and the 403-is-rate-limit classification
// (mirroring Search) live in the base DoDownload: a grab error surfaces at most the
// download endpoint's scheme://host — never the passkey — and the bytes go to /dl,
// never a log. GrabDirect (Base) owns the shared build-GET/DoDownload/GrabResult shape;
// ClassifyRateLimit403 is this family's dialect (403 is a spent query budget, not auth).
func (d *driver) Grab(ctx context.Context, link string) (*search.GrabResult, error) {
	return d.GrabDirect(ctx, link, native.ClassifyRateLimit403)
}
