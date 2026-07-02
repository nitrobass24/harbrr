package hdbits

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
// because a large pack carries megabytes of piece hashes; readCapped errors rather than
// silently truncating a corrupt torrent.
const maxTorrentBytes = 64 << 20

var (
	errDownloadTooLarge = errors.New("hdbits: download exceeds the size cap")
	// errDownloadRequestFailed is the fixed, link-free transport failure: the download URL
	// embeds the passkey, so the underlying *url.Error (which echoes the URL) is never
	// surfaced. get() returns this directly so the secret cannot leak through %w.
	errDownloadRequestFailed = errors.New("hdbits: download request failed")
)

// Grab fetches the rebuilt download.php URL server-side and returns the .torrent bytes.
// The download URL embeds the passkey in its query (download.php?id=…&passkey=…), which
// *arr must not see, which is why NeedsResolver is true and the served feed routes the
// download through the /dl proxy; this is the server-side fetch /dl drives, so the
// passkey-bearing URL never reaches the feed. The URL already carries its own passkey,
// so no auth header is set. The download is a direct torrent (never a magnet), so
// Redirect is empty. No error carries the download URL (its passkey sits in the query),
// and the bytes go to /dl, never a log.
func (d *driver) Grab(ctx context.Context, link string) (*search.GrabResult, error) {
	resp, err := d.get(ctx, link)
	if err != nil {
		return nil, sanitizeGrabError(err)
	}
	defer func() { _ = resp.Body.Close() }()

	switch {
	case resp.StatusCode == stdhttp.StatusUnauthorized:
		return nil, fmt.Errorf("hdbits: download unauthorized: %w", login.ErrLoginFailed)
	case resp.StatusCode == stdhttp.StatusForbidden || search.IsRateLimitStatus(resp.StatusCode):
		// 403 is HDBits' query/rate-limit (Prowlarr's RequestLimitReached), not an auth
		// failure, so it backs off like 429/503 (mirrors Search).
		return nil, &search.RateLimitedError{
			StatusCode: resp.StatusCode,
			RetryAfter: search.ParseRetryAfter(resp.Header.Get("Retry-After"), d.clock),
		}
	case resp.StatusCode < 200 || resp.StatusCode >= 300:
		return nil, fmt.Errorf("hdbits: download returned HTTP %d", resp.StatusCode)
	}

	body, err := readCapped(resp.Body, maxTorrentBytes)
	if err != nil {
		return nil, sanitizeGrabError(err)
	}
	return &search.GrabResult{
		Body:        body,
		ContentType: resp.Header.Get("Content-Type"),
	}, nil
}

// get issues a plain GET for an HDBits download URL. The URL already carries its own
// passkey in the query (download.php?id=…&passkey=…), so no auth header is needed. The
// transport error from Do is a *url.Error whose Error() embeds the FULL unredacted URL, so
// it is NOT interpolated here: get() returns a fixed, link-free error so the passkey cannot
// re-leak through %w regardless of who calls get() (the contract does not depend on the
// caller scrubbing). The caller owns the returned body and interprets the status.
func (d *driver) get(ctx context.Context, rawurl string) (*stdhttp.Response, error) {
	req, err := stdhttp.NewRequestWithContext(ctx, stdhttp.MethodGet, rawurl, nil)
	if err != nil {
		return nil, errDownloadRequestFailed
	}
	resp, err := d.doer.Do(req)
	if err != nil {
		// The transport error carries the passkey-bearing URL (via *url.Error, whose
		// Error() embeds the full URL), so it is never interpolated. A context
		// cancellation/deadline must stay detectable by the caller, so those sentinels are
		// preserved (errors.Is sees through *url.Error); every other transport error is
		// flattened to a fixed, link-free message.
		if errors.Is(err, context.Canceled) {
			return nil, context.Canceled
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, context.DeadlineExceeded
		}
		return nil, errDownloadRequestFailed
	}
	return resp, nil
}

// sanitizeGrabError strips a possibly passkey-bearing transport error: the download URL
// carries the passkey in its query, so any non-sentinel error from the fetch is replaced
// with a fixed, link-free message rather than risk surfacing the URL. Sentinels that carry
// no URL and that callers need to classify are passed through unchanged: auth and
// rate-limit (for health), context cancellation/deadline (so normal cancellation is not
// misreported as a failure), and the size-cap error.
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
	return errDownloadRequestFailed
}

// readCapped reads up to limit bytes, returning errDownloadTooLarge when the source
// exceeds the cap rather than silently truncating (a truncated .torrent is corrupt). The
// returned errors never carry the source URL.
func readCapped(r io.Reader, limit int64) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, fmt.Errorf("hdbits: read download response: %w", err)
	}
	if int64(len(body)) > limit {
		return nil, errDownloadTooLarge
	}
	return body, nil
}
