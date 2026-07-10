package myanonamouse

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

// maxTorrentBytes caps a fetched .torrent. It is far larger than the search JSON cap
// because a large pack carries megabytes of piece hashes; readCapped errors rather
// than silently truncating a corrupt torrent.
const maxTorrentBytes = 64 << 20

var errDownloadTooLarge = errors.New("myanonamouse: download exceeds the size cap")

// Grab fetches the resolved download URL with the mam_id Cookie and returns the
// .torrent bytes. *arr cannot send the Cookie, which is why NeedsResolver is true and
// the served feed routes the download through the /dl proxy; this is the server-side
// fetch /dl drives, so neither the cookie nor any key in the download URL reaches the
// feed. The download is a direct torrent (never a magnet), so Redirect is empty. A grab
// error surfaces at most the download endpoint's scheme://host (never the mam_id, which
// rides a header, nor a secret in the URL's path/query), and the bytes go to /dl, never a
// log.
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
	case resp.StatusCode == stdhttp.StatusForbidden:
		return nil, fmt.Errorf("myanonamouse: download forbidden, mam_id expired or invalid: %w", login.ErrLoginFailed)
	case resp.StatusCode < 200 || resp.StatusCode >= 300:
		return nil, fmt.Errorf("myanonamouse: download returned HTTP %d", resp.StatusCode)
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

// sanitizeGrabError classifies a grab-path error. The auth and rate-limit sentinels are
// passed through unchanged for health classification. Everything else falls through to a
// %w-wrap of the cause, which is host-only: it is get()'s transport error, already rebuilt
// host-only by construction (apphttp.SchemeHost + apphttp.RedactURLError drop the download
// URL's path and query). Routing the fallback through apphttp.RedactURLError additionally
// rebuilds a stray build-request *url.Error host-only (get()'s NewRequestWithContext branch
// wraps it bare), so the download link's secret path/query never surfaces — only its
// scheme://host can.
func sanitizeGrabError(err error) error {
	if errors.Is(err, login.ErrLoginFailed) {
		return err
	}
	var rl *search.RateLimitedError
	if errors.As(err, &rl) {
		return err
	}
	return fmt.Errorf("myanonamouse: download request failed: %w", apphttp.RedactURLError(err))
}

// readCapped reads up to limit bytes, returning errDownloadTooLarge when the source
// exceeds the cap rather than silently truncating (a truncated .torrent is corrupt).
// The returned errors never carry the source URL.
func readCapped(r io.Reader, limit int64) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, fmt.Errorf("myanonamouse: read download response: %w", err)
	}
	if int64(len(body)) > limit {
		return nil, errDownloadTooLarge
	}
	return body, nil
}
