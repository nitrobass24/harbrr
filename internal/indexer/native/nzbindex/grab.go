package nzbindex

import (
	"context"
	"errors"
	"fmt"
	stdhttp "net/http"

	apphttp "github.com/autobrr/harbrr/internal/http"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/login"
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
// application/x-nzb so the serve path tags the body correctly.
func (d *driver) Grab(ctx context.Context, link string) (*search.GrabResult, error) {
	resp, err := d.fetch(ctx, link)
	if err != nil {
		if resp != nil {
			return nil, err
		}
		return nil, sanitizeGrabError(err)
	}
	return &search.GrabResult{Body: resp.Body, ContentType: nzbContentType}, nil
}

// fetch issues a plain GET for a .nzb download URL. A transport error is a *url.Error whose
// Error() embeds the full URL, so it is routed through apphttp.RedactURLError and wrapped
// under errDownloadRequestFailed (scheme://host only). Context sentinels are preserved.
func (d *driver) fetch(ctx context.Context, rawurl string) (*native.Response, error) {
	req, err := stdhttp.NewRequestWithContext(ctx, stdhttp.MethodGet, rawurl, nil)
	if err != nil {
		return nil, errDownloadRequestFailed
	}
	resp, err := d.DoDownload(ctx, req, native.ClassifyRateLimit403)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return nil, context.Canceled
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, context.DeadlineExceeded
		}
		if resp != nil {
			return resp, err
		}
		return nil, fmt.Errorf("%w: %w", errDownloadRequestFailed, apphttp.RedactURLError(err))
	}
	return resp, nil
}

// sanitizeGrabError classifies a grab error for surfacing: auth, rate-limit, context, and
// size-cap sentinels pass through; an already-enriched errDownloadRequestFailed passes
// through verbatim; anything else is flattened to the bare sentinel rather than risk a URL.
func sanitizeGrabError(err error) error {
	switch {
	case errors.Is(err, login.ErrLoginFailed),
		errors.Is(err, context.Canceled),
		errors.Is(err, context.DeadlineExceeded),
		errors.Is(err, native.ErrDownloadTooLarge):
		return err
	}
	var rl *search.RateLimitedError
	if errors.As(err, &rl) {
		return err
	}
	if errors.Is(err, errDownloadRequestFailed) {
		return err
	}
	return errDownloadRequestFailed
}
