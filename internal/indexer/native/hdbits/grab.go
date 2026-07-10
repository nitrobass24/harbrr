package hdbits

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
// because a large pack carries megabytes of piece hashes; readCapped errors rather than
// silently truncating a corrupt torrent.
const maxTorrentBytes = 64 << 20

var (
	errDownloadTooLarge = errors.New("hdbits: download exceeds the size cap")
	// errDownloadRequestFailed is the grab-path transport failure. The download URL embeds the
	// passkey in its query, so the underlying *url.Error (which echoes the full URL) is never
	// surfaced verbatim: get()'s transport branch routes it through apphttp.RedactURLError and
	// wraps only the host-only cause, so %w can re-expose at most "scheme://host" — never the
	// passkey. The build-request branch and readCapped's io failures return it bare.
	errDownloadRequestFailed = errors.New("hdbits: download request failed")
)

// Grab fetches the rebuilt download.php URL server-side and returns the .torrent bytes.
// The download URL embeds the passkey in its query (download.php?id=…&passkey=…), which
// *arr must not see, which is why NeedsResolver is true and the served feed routes the
// download through the /dl proxy; this is the server-side fetch /dl drives, so the
// passkey-bearing URL never reaches the feed. The URL already carries its own passkey,
// so no auth header is set. The download is a direct torrent (never a magnet), so
// Redirect is empty. A grab error surfaces at most the download endpoint's scheme://host
// (never the passkey, which sits in the query), and the bytes go to /dl, never a log.
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
// it is routed through apphttp.RedactURLError first: get() wraps only the host-only cause
// into errDownloadRequestFailed, so %w can re-expose at most "scheme://host" — never the
// passkey — regardless of who calls get() (the contract does not depend on the caller
// scrubbing). The caller owns the returned body and interprets the status.
func (d *driver) get(ctx context.Context, rawurl string) (*stdhttp.Response, error) {
	req, err := stdhttp.NewRequestWithContext(ctx, stdhttp.MethodGet, rawurl, nil)
	if err != nil {
		return nil, errDownloadRequestFailed
	}
	resp, err := d.doer.Do(req)
	if err != nil {
		// The transport error carries the passkey-bearing URL (via *url.Error, whose
		// Error() embeds the full URL), so it is redacted to a host-only cause before
		// wrapping. A context cancellation/deadline must stay detectable by the caller, so
		// those sentinels are preserved (errors.Is sees through *url.Error); every other
		// transport error is wrapped into errDownloadRequestFailed with only its
		// scheme://host surfaced.
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

// sanitizeGrabError classifies a grab-path error. Sentinels that carry no URL and that
// callers need to classify are passed through unchanged: auth and rate-limit (for health),
// context cancellation/deadline (so normal cancellation is not misreported as a failure),
// and the size-cap error. An errDownloadRequestFailed — get()'s already-host-only transport
// failure, or its bare build-request variant — is returned verbatim so its host-only cause
// is preserved rather than collapsed or double-prefixed. Anything else (e.g. an unexpected
// io read error that might carry free text) is flattened to the bare errDownloadRequestFailed
// rather than risk surfacing an unredacted string.
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
