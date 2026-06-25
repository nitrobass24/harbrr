package passthepopcorn

import (
	"context"
	"errors"
	"fmt"
	"io"
	stdhttp "net/http"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/login"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
)

// maxTorrentBytes caps a fetched .torrent. It is far larger than the search JSON cap
// because a large box-set carries megabytes of piece hashes; readTorrent errors rather
// than silently truncating a corrupt torrent.
const maxTorrentBytes = 64 << 20

var errDownloadTooLarge = errors.New("passthepopcorn: download exceeds the size cap")

// errNotTorrent flags a 2xx response whose body is not bencode (a .torrent always begins
// with 'd', a bencoded dictionary). PTP can answer a download with HTTP 200 yet serve a
// JSON error page (e.g. a query-limit notice), so a non-bencode success is rejected
// rather than handed downstream as a corrupt torrent.
var errNotTorrent = errors.New("passthepopcorn: download response is not a torrent")

// Grab fetches the PTP download URL (torrents.php?action=download&id=<id>) server-side and
// returns the .torrent bytes. The link carries no secret — the ApiUser/ApiKey credentials
// ride in headers, attached by get — so the served feed exposes the link and routes the
// fetch through the /dl proxy, which is the server-side fetch this Grab drives
// (DownloadNeedsAuth is true, NeedsResolver is false; the Gazelle model). The download is a
// direct torrent (never a magnet), so Redirect is empty. A 401 maps to login.ErrLoginFailed;
// a 403 (PTP's query-limit) or a 429/503 maps to a RateLimitedError (the parity target
// raises RequestLimitReachedException on 403 — a transient pacing signal, not bad creds);
// any other non-2xx is an error; transport and read errors pass through sanitizeGrabError so
// no URL or credential surfaces. The bytes go to /dl, never a log.
func (d *driver) Grab(ctx context.Context, link string) (*search.GrabResult, error) {
	resp, err := d.get(ctx, link, "")
	if err != nil {
		return nil, sanitizeGrabError(err)
	}
	defer func() { _ = resp.Body.Close() }()

	switch {
	case resp.StatusCode == stdhttp.StatusUnauthorized:
		return nil, fmt.Errorf("passthepopcorn: download unauthorized: %w", login.ErrLoginFailed)
	case resp.StatusCode == stdhttp.StatusForbidden || search.IsRateLimitStatus(resp.StatusCode):
		return nil, &search.RateLimitedError{
			StatusCode: resp.StatusCode,
			RetryAfter: search.ParseRetryAfter(resp.Header.Get("Retry-After"), d.clock),
		}
	case resp.StatusCode < 200 || resp.StatusCode >= 300:
		return nil, fmt.Errorf("passthepopcorn: download returned HTTP %d", resp.StatusCode)
	}

	body, err := readTorrent(resp.Body, maxTorrentBytes)
	if err != nil {
		return nil, sanitizeGrabError(err)
	}
	// A .torrent is a bencoded dictionary, which always begins with 'd'. PTP can return
	// a 2xx whose body is a JSON error page instead of a torrent; reject that here so a
	// non-torrent never reaches qBittorrent.
	if len(body) == 0 || body[0] != 'd' {
		return nil, errNotTorrent
	}
	return &search.GrabResult{
		Body:        body,
		ContentType: resp.Header.Get("Content-Type"),
	}, nil
}

// Test exercises the credentials with an empty browse query: a 401 surfaces as
// login.ErrLoginFailed (the registry records an auth_failure health event), a 403/429/503
// surfaces as a RateLimitedError, while a parseable response confirms the credentials work.
// Reuses Search so the test path is the real request path, including the status mapping and
// header auth.
func (d *driver) Test(ctx context.Context) error {
	_, err := d.Search(ctx, search.Query{})
	return err
}

// sanitizeGrabError strips a possibly URL-bearing transport error to a fixed, link-free
// message. The download link carries no secret, but a verbose transport error may still
// echo the host or full URL, so non-sentinel errors are replaced. Sentinels callers need
// to classify pass through unchanged: auth and rate-limit (for health), context
// cancellation/deadline (so a cancelled request is not misreported as a failure), and the
// size-cap error.
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
	return errors.New("passthepopcorn: download request failed")
}

// readTorrent reads up to limit bytes, returning errDownloadTooLarge when the source
// exceeds the cap rather than silently truncating (a truncated .torrent is corrupt). The
// returned errors never carry the source URL.
func readTorrent(r io.Reader, limit int64) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, fmt.Errorf("passthepopcorn: read download response: %w", err)
	}
	if int64(len(body)) > limit {
		return nil, errDownloadTooLarge
	}
	return body, nil
}
