package beyondhd

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
// because a large season pack carries megabytes of piece hashes; readCapped errors
// rather than silently truncating a corrupt torrent.
const maxTorrentBytes = 64 << 20

var errDownloadTooLarge = errors.New("beyondhd: download exceeds the size cap")

// Grab fetches the BeyondHD download_url server-side and returns the .torrent bytes. The
// URL embeds the rsskey in its PATH (torrent/download/auto.<id>.<rsskey>), which *arr must
// not see, which is why NeedsResolver is true and the served feed routes the download
// through the /dl proxy; this is the server-side fetch /dl drives, so the credential-bearing
// URL never reaches the feed. The download is a direct torrent (never a magnet), so Redirect
// is empty. No auth header is needed — the rsskey rides in the URL — so the GET is plain.
// No error carries the rsskey-bearing download URL: a build or transport error surfaces
// only its scheme://host (apphttp.SchemeHost drops the PATH where the rsskey lives), and
// sanitizeGrabError wraps that host-only cause rather than the raw URL.
func (d *driver) Grab(ctx context.Context, link string) (*search.GrabResult, error) {
	resp, err := d.get(ctx, link)
	if err != nil {
		return nil, sanitizeGrabError(err)
	}
	defer func() { _ = resp.Body.Close() }()

	switch {
	case resp.StatusCode == stdhttp.StatusUnauthorized || resp.StatusCode == stdhttp.StatusForbidden:
		return nil, fmt.Errorf("beyondhd: download unauthorized: %w", login.ErrLoginFailed)
	case search.IsRateLimitStatus(resp.StatusCode):
		return nil, &search.RateLimitedError{
			StatusCode: resp.StatusCode,
			RetryAfter: search.ParseRetryAfter(resp.Header.Get("Retry-After"), d.clock),
		}
	case resp.StatusCode < 200 || resp.StatusCode >= 300:
		return nil, fmt.Errorf("beyondhd: download returned HTTP %d", resp.StatusCode)
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

// get issues a plain GET for a BeyondHD download URL. The URL already carries its own rsskey
// in its PATH (no auth header is needed for the download), so no header is set. The URL is
// secret-bearing, so a build or transport error surfaces only its scheme://host
// (apphttp.SchemeHost drops the PATH where the rsskey lives; apphttp.RedactURLError rebuilds
// the cause host-only). The caller owns the returned body.
func (d *driver) get(ctx context.Context, rawurl string) (*stdhttp.Response, error) {
	req, err := stdhttp.NewRequestWithContext(ctx, stdhttp.MethodGet, rawurl, nil)
	if err != nil {
		return nil, fmt.Errorf("beyondhd: build download request: %w", apphttp.RedactURLError(err))
	}
	resp, err := d.doer.Do(req)
	if err != nil {
		return nil, fmt.Errorf("beyondhd: request to %s: %w", apphttp.SchemeHost(rawurl), apphttp.RedactURLError(err))
	}
	return resp, nil
}

// sanitizeGrabError classifies a Grab error. Sentinels that carry no URL and that callers
// need to classify are passed through unchanged: auth and rate-limit (for health), context
// cancellation/deadline (so normal cancellation is not misreported as a failure), and the
// size-cap error. Any other error is wrapped under a fixed "download request failed" prefix.
//
// The fallback %w-wraps the cause, which is host-only: either get()'s transport error
// (host-only by construction — it surfaces only apphttp.SchemeHost(rawurl) and routes its
// cause through apphttp.RedactURLError, both of which drop the PATH where the rsskey lives)
// or readCapped's io read error (URL-free). apphttp.RedactURLError additionally rebuilds a
// stray build-request *url.Error host-only, so the download link's secret path/query never
// surfaces (only its scheme://host can).
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
	return fmt.Errorf("beyondhd: download request failed: %w", apphttp.RedactURLError(err))
}

// readCapped reads up to limit bytes, returning errDownloadTooLarge when the source exceeds
// the cap rather than silently truncating (a truncated .torrent is corrupt). The returned
// errors never carry the source URL.
func readCapped(r io.Reader, limit int64) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, fmt.Errorf("beyondhd: read download response: %w", err)
	}
	if int64(len(body)) > limit {
		return nil, errDownloadTooLarge
	}
	return body, nil
}
