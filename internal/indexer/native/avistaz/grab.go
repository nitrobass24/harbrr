package avistaz

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

// maxTorrentBytes caps a fetched .torrent. It is far larger than the search/auth JSON
// cap because a large pack carries megabytes of piece hashes; readCapped errors rather
// than silently truncating a corrupt torrent.
const maxTorrentBytes = 64 << 20

var errDownloadTooLarge = errors.New("avistaz: download exceeds the size cap")

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
	resp, err := d.get(ctx, link, "")
	if err != nil {
		return nil, sanitizeGrabError(err)
	}
	defer func() { _ = resp.Body.Close() }()

	switch {
	case search.IsRateLimitStatus(resp.StatusCode):
		return nil, &search.RateLimitedError{
			StatusCode: resp.StatusCode,
			RetryAfter: search.ParseRetryAfter(resp.Header.Get("Retry-After"), d.clock),
		}
	case resp.StatusCode == stdhttp.StatusUnauthorized || resp.StatusCode == stdhttp.StatusPreconditionFailed:
		return nil, fmt.Errorf("avistaz: download unauthorized: %w", login.ErrLoginFailed)
	case resp.StatusCode < 200 || resp.StatusCode >= 300:
		return nil, fmt.Errorf("avistaz: download returned HTTP %d", resp.StatusCode)
	}

	body, err := readCapped(resp.Body, maxTorrentBytes)
	if err != nil {
		return nil, err
	}
	return &search.GrabResult{
		Body:        body,
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
	var rl *search.RateLimitedError
	if errors.As(err, &rl) {
		return err
	}
	return fmt.Errorf("avistaz: download request failed: %w", apphttp.RedactURLError(err))
}

// readCapped reads up to max bytes, returning errDownloadTooLarge when the source
// exceeds the cap rather than silently truncating (a truncated .torrent is corrupt).
// The returned errors never carry the source URL.
func readCapped(r io.Reader, limit int64) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, fmt.Errorf("avistaz: read download response: %w", err)
	}
	if int64(len(body)) > limit {
		return nil, errDownloadTooLarge
	}
	return body, nil
}
