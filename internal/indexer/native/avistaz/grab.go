package avistaz

import (
	"context"
	"errors"
	"fmt"

	apphttp "github.com/autobrr/harbrr/internal/http"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/login"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
	"github.com/autobrr/harbrr/internal/indexer/native"
)

// Grab fetches the resolved AvistaZ download URL with the Bearer header and returns
// the .torrent bytes. *arr cannot send the Bearer, which is why NeedsResolver is true
// and the served feed routes the download through the /dl proxy; this is the
// server-side fetch /dl drives, so neither the Bearer nor any key in the download URL
// reaches the feed. The download is a direct torrent (never a magnet), so Redirect is
// empty. A transport error surfaces only the scheme://host (sanitizeGrabError routes it
// through RedactURLError); the download URL's key — which may sit in its path, beyond the
// reach of the query-scoped URL redactor — never surfaces, and the bytes go to /dl, never
// a log.
func (d *driver) Grab(ctx context.Context, link string) (*search.GrabResult, error) {
	resp, err := d.get(ctx, link, "", true)
	if err != nil {
		if resp != nil {
			return nil, err
		}
		return nil, sanitizeGrabError(err)
	}
	return &search.GrabResult{
		Body:        resp.Body,
		ContentType: resp.Header.Get("Content-Type"),
	}, nil
}

// sanitizeGrabError classifies a grab error. The auth (for health) and rate-limit
// sentinels — which carry no download URL — pass through unchanged for classification.
// Every other error hits the fallback, which %w-wraps its cause, and that cause is
// host-only: get()'s transport error is host-only by construction (SchemeHost +
// RedactURLError drop the key-bearing path and query), and any io read error is URL-free.
// RedactURLError additionally rebuilds a stray build-request *url.Error host-only, so the
// download link's secret path/query never surfaces — only its scheme://host can.
func sanitizeGrabError(err error) error {
	if errors.Is(err, login.ErrLoginFailed) {
		return err
	}
	if errors.Is(err, native.ErrDownloadTooLarge) {
		return err
	}
	var rl *search.RateLimitedError
	if errors.As(err, &rl) {
		return err
	}
	return fmt.Errorf("avistaz: download request failed: %w", apphttp.RedactURLError(err))
}
