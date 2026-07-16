package newznab

import (
	"context"
	"errors"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
	"github.com/autobrr/harbrr/internal/indexer/native"
)

// nzbContentType is what the /dl proxy serves a fetched .nzb as. harbrr's torrent
// content-type constant (search.torrentContentType) is torrent-specific, so the Newznab
// driver sets its own.
const nzbContentType = "application/x-nzb"

// errDownloadRequestFailed is the transport-failure sentinel. A build-request failure
// returns it bare (there is no URL to leak). A transport failure from Do wraps it with a
// HOST-ONLY cause (apphttp.RedactURLError drops the apikey-bearing path/query), so the
// scheme://host surfaces for diagnosis while the apikey cannot re-leak through %w.
var errDownloadRequestFailed = errors.New("newznab: download request failed")

// Grab fetches the .nzb body server-side and returns it as a GrabResult. The download URL
// embeds the apikey, which the *arr/SABnzbd must not see, which is why DownloadNeedsAuth is
// true and the served feed routes the download through the /dl proxy; this is the
// server-side fetch /dl drives, so the apikey-bearing URL never reaches the feed. The result
// is ALWAYS a Body (an .nzb is a direct download), NEVER a Redirect — redirecting an
// apikey-bearing URL would leak the secret to the downstream client. ContentType is
// application/x-nzb so the serializer/serve path tags the body correctly. No error carries
// the download URL — a transport failure surfaces only its scheme://host (the apikey sits in
// the path/query, which is dropped) — and the bytes go to /dl, never a log. GrabNZB (Base)
// owns the shared fetch/sanitize shape common to the usenet pair (newznab, nzbindex).
func (d *driver) Grab(ctx context.Context, link string) (*search.GrabResult, error) {
	return d.GrabNZB(ctx, link, nzbContentType, native.ClassifyRateLimit403, errDownloadRequestFailed)
}
