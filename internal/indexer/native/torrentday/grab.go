package torrentday

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

// maxTorrentBytes caps a fetched .torrent. It is far larger than the search/test JSON cap
// because a large pack carries megabytes of piece hashes; readCapped errors rather than
// silently truncating a corrupt torrent.
const maxTorrentBytes = 64 << 20

var errDownloadTooLarge = errors.New("torrentday: download exceeds the size cap")

// Grab fetches the resolved TorrentDay download URL (download.php/<id>/<id>.torrent) with
// the session cookie and returns the .torrent bytes. *arr cannot send that cookie, which
// is why NeedsResolver is true and the served feed routes the download through /dl; this
// is the server-side fetch /dl drives, so neither the cookie nor the download URL reaches
// the feed. The download is a direct torrent (never a magnet), so Redirect is empty. No
// error carries the download URL or cookie, and the bytes go to /dl, never a log. The
// context is stamped WithNoRedirectFollow so a stale-cookie redirect to /login.php
// surfaces as an auth failure (isLoginRedirect) rather than being followed.
func (d *driver) Grab(ctx context.Context, link string) (*search.GrabResult, error) {
	resp, err := d.get(apphttp.WithNoRedirectFollow(ctx), link, "")
	if err != nil {
		return nil, sanitizeGrabError(err)
	}
	defer func() { _ = resp.Body.Close() }()

	switch {
	case search.IsRateLimitStatus(resp.StatusCode):
		return nil, &search.RateLimitedError{StatusCode: resp.StatusCode, RetryAfter: d.parseRetryAfter(resp)}
	case isLoginRedirect(resp):
		return nil, fmt.Errorf("torrentday: download redirected to login: %w", login.ErrLoginFailed)
	case resp.StatusCode == stdhttp.StatusUnauthorized || resp.StatusCode == stdhttp.StatusForbidden:
		return nil, fmt.Errorf("torrentday: download unauthorized: %w", login.ErrLoginFailed)
	case resp.StatusCode < 200 || resp.StatusCode >= 300:
		return nil, fmt.Errorf("torrentday: download returned HTTP %d", resp.StatusCode)
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

// sanitizeGrabError strips a possibly link-bearing transport error: a download URL carries
// the torrent id in its path, which the query-scoped URL redactor cannot reach, so any
// non-sentinel error from the fetch is replaced with a fixed message. The scrubbedError
// from get already strips the cookie, but a fixed message is the belt-and-suspenders that
// also drops the URL. Sentinels that carry no URL and that callers need to classify are
// passed through unchanged: auth and rate-limit (for health), context cancellation/deadline
// (so normal cancellation is not misreported as a failure), and the size-cap error.
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
	return errors.New("torrentday: download request failed")
}

// readCapped reads up to limit bytes, returning errDownloadTooLarge when the source
// exceeds the cap rather than silently truncating (a truncated .torrent is corrupt). The
// returned errors never carry the source URL.
func readCapped(r io.Reader, limit int64) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, fmt.Errorf("torrentday: read download response: %w", err)
	}
	if int64(len(body)) > limit {
		return nil, errDownloadTooLarge
	}
	return body, nil
}
