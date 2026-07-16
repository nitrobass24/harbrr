package nzbindex

import (
	"context"
	"errors"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
	"github.com/autobrr/harbrr/internal/indexer/native"
)

// nzbContentType is what a fetched .nzb is served as.
const nzbContentType = "application/x-nzb"

var errDownloadRequestFailed = errors.New("nzbindex: download request failed")

// Grab fetches the .nzb body server-side and returns it as a GrabResult. NZBIndex download
// links are public and carry no secret, so DownloadNeedsAuth is false and the feed normally
// serves the link bare; this method backs the /dl proxy if a caller routes through it. The
// result is always a Body (an .nzb is a direct download), never a Redirect. ContentType is
// application/x-nzb so the serve path tags the body correctly. GrabNZB (Base) owns the
// shared fetch/sanitize shape common to the usenet pair (newznab, nzbindex).
func (d *driver) Grab(ctx context.Context, link string) (*search.GrabResult, error) {
	return d.GrabNZB(ctx, link, nzbContentType, native.ClassifyRateLimit403, errDownloadRequestFailed)
}
