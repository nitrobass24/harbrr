package filelist

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

var errDownloadTooLarge = errors.New("filelist: download exceeds the size cap")

// Grab fetches the rebuilt download.php URL with the Basic header and returns the
// .torrent bytes. The download URL carries the passkey in its query, which *arr must
// not see, which is why NeedsResolver is true and the served feed routes the download
// through the /dl proxy; this is the server-side fetch /dl drives, so neither the
// Basic header nor the passkey in the URL reaches the feed. The download is a direct
// torrent (never a magnet), so Redirect is empty. On a fetch failure the error
// surfaces only the download URL's scheme://host (its secret path/query never surface
// — see sanitizeGrabError), and the bytes go to /dl, never a log.
func (d *driver) Grab(ctx context.Context, link string) (*search.GrabResult, error) {
	resp, err := d.get(ctx, link, "")
	if err != nil {
		return nil, sanitizeGrabError(err)
	}
	defer func() { _ = resp.Body.Close() }()

	switch {
	case resp.StatusCode == stdhttp.StatusUnauthorized || resp.StatusCode == stdhttp.StatusForbidden:
		return nil, fmt.Errorf("filelist: download unauthorized: %w", login.ErrLoginFailed)
	case search.IsRateLimitStatus(resp.StatusCode):
		return nil, &search.RateLimitedError{
			StatusCode: resp.StatusCode,
			RetryAfter: search.ParseRetryAfter(resp.Header.Get("Retry-After"), d.clock),
		}
	case resp.StatusCode < 200 || resp.StatusCode >= 300:
		return nil, fmt.Errorf("filelist: download returned HTTP %d", resp.StatusCode)
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

// sanitizeGrabError maps a download-fetch error to a stable, secret-safe error. Auth
// and rate-limit sentinels are kept for health classification; every other cause hits
// the fallback, which %w-wraps a host-only cause: get()'s transport error is host-only
// by construction (get already rebuilds the *url.Error via SchemeHost), an io read
// error is URL-free, and RedactURLError additionally rebuilds a stray build-request
// *url.Error host-only. So the download link's secret path/query never surface — only
// its scheme://host can, and the host is not a secret.
func sanitizeGrabError(err error) error {
	if errors.Is(err, login.ErrLoginFailed) {
		return err
	}
	var rl *search.RateLimitedError
	if errors.As(err, &rl) {
		return err
	}
	return fmt.Errorf("filelist: download request failed: %w", apphttp.RedactURLError(err))
}

// readCapped reads up to limit bytes, returning errDownloadTooLarge when the source
// exceeds the cap rather than silently truncating (a truncated .torrent is corrupt).
// The returned errors never carry the source URL.
func readCapped(r io.Reader, limit int64) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, fmt.Errorf("filelist: read download response: %w", err)
	}
	if int64(len(body)) > limit {
		return nil, errDownloadTooLarge
	}
	return body, nil
}
