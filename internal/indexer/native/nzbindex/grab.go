package nzbindex

import (
	"context"
	"errors"
	"fmt"
	"io"
	stdhttp "net/http"

	apphttp "github.com/autobrr/harbrr/internal/http"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/login"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
)

// nzbContentType is what a fetched .nzb is served as.
const nzbContentType = "application/x-nzb"

// maxNZBBytes caps a fetched .nzb. An .nzb is a small XML pointer file (segment ids, not the
// article bodies), so even a large multi-file post is well under this.
const maxNZBBytes = 64 << 20

var (
	errDownloadTooLarge      = errors.New("nzbindex: download exceeds the size cap")
	errDownloadRequestFailed = errors.New("nzbindex: download request failed")
)

// Grab fetches the .nzb body server-side and returns it as a GrabResult. NZBIndex download
// links are public and carry no secret, so DownloadNeedsAuth is false and the feed normally
// serves the link bare; this method backs the /dl proxy if a caller routes through it. The
// result is always a Body (an .nzb is a direct download), never a Redirect. ContentType is
// application/x-nzb so the serve path tags the body correctly.
func (d *driver) Grab(ctx context.Context, link string) (*search.GrabResult, error) {
	resp, err := d.fetch(ctx, link)
	if err != nil {
		return nil, sanitizeGrabError(err)
	}
	defer func() { _ = resp.Body.Close() }()

	switch {
	case resp.StatusCode == stdhttp.StatusUnauthorized:
		return nil, fmt.Errorf("nzbindex: download unauthorized: %w", login.ErrLoginFailed)
	case resp.StatusCode == stdhttp.StatusForbidden || search.IsRateLimitStatus(resp.StatusCode):
		return nil, &search.RateLimitedError{
			StatusCode: resp.StatusCode,
			RetryAfter: search.ParseRetryAfter(resp.Header.Get("Retry-After"), d.clock),
		}
	case resp.StatusCode < 200 || resp.StatusCode >= 300:
		return nil, fmt.Errorf("nzbindex: download returned HTTP %d", resp.StatusCode)
	}

	body, err := readCapped(resp.Body, maxNZBBytes)
	if err != nil {
		return nil, sanitizeGrabError(err)
	}
	return &search.GrabResult{Body: body, ContentType: nzbContentType}, nil
}

// fetch issues a plain GET for a .nzb download URL. A transport error is a *url.Error whose
// Error() embeds the full URL, so it is routed through apphttp.RedactURLError and wrapped
// under errDownloadRequestFailed (scheme://host only). Context sentinels are preserved.
func (d *driver) fetch(ctx context.Context, rawurl string) (*stdhttp.Response, error) {
	req, err := stdhttp.NewRequestWithContext(ctx, stdhttp.MethodGet, rawurl, nil)
	if err != nil {
		return nil, errDownloadRequestFailed
	}
	resp, err := d.doer.Do(req)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return nil, context.Canceled
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, context.DeadlineExceeded
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
		errors.Is(err, errDownloadTooLarge):
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

// readCapped reads up to limit bytes, erroring rather than silently truncating a corrupt
// .nzb. Errors never carry the source URL (a read error is scrubbed through RedactError).
func readCapped(r io.Reader, limit int64) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, fmt.Errorf("nzbindex: read download response: %s", apphttp.RedactError(err))
	}
	if int64(len(body)) > limit {
		return nil, errDownloadTooLarge
	}
	return body, nil
}
