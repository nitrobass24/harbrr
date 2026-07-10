package newznab

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

// nzbContentType is what the /dl proxy serves a fetched .nzb as. harbrr's torrent
// content-type constant (search.torrentContentType) is torrent-specific, so the Newznab
// driver sets its own.
const nzbContentType = "application/x-nzb"

// maxNZBBytes caps a fetched .nzb. An .nzb is a small XML pointer file (segment ids, not
// the article bodies), so even a large multi-file post is well under this; readCapped errors
// rather than silently truncating a corrupt nzb.
const maxNZBBytes = 64 << 20

var (
	errDownloadTooLarge = errors.New("newznab: download exceeds the size cap")
	// errDownloadRequestFailed is the transport-failure sentinel. A build-request failure
	// returns it bare (there is no URL to leak). A transport failure from Do wraps it with a
	// HOST-ONLY cause (apphttp.RedactURLError drops the apikey-bearing path/query), so the
	// scheme://host surfaces for diagnosis while the apikey cannot re-leak through %w.
	errDownloadRequestFailed = errors.New("newznab: download request failed")
)

// Grab fetches the .nzb body server-side and returns it as a GrabResult. The download URL
// embeds the apikey, which the *arr/SABnzbd must not see, which is why DownloadNeedsAuth is
// true and the served feed routes the download through the /dl proxy; this is the
// server-side fetch /dl drives, so the apikey-bearing URL never reaches the feed. The result
// is ALWAYS a Body (an .nzb is a direct download), NEVER a Redirect — redirecting an
// apikey-bearing URL would leak the secret to the downstream client. ContentType is
// application/x-nzb so the serializer/serve path tags the body correctly. No error carries
// the download URL — a transport failure surfaces only its scheme://host (the apikey sits in
// the path/query, which is dropped) — and the bytes go to /dl, never a log.
func (d *driver) Grab(ctx context.Context, link string) (*search.GrabResult, error) {
	resp, err := d.fetch(ctx, link)
	if err != nil {
		return nil, sanitizeGrabError(err)
	}
	defer func() { _ = resp.Body.Close() }()

	switch {
	case resp.StatusCode == stdhttp.StatusUnauthorized:
		return nil, fmt.Errorf("newznab: download unauthorized: %w", login.ErrLoginFailed)
	case resp.StatusCode == stdhttp.StatusForbidden || search.IsRateLimitStatus(resp.StatusCode):
		return nil, &search.RateLimitedError{
			StatusCode: resp.StatusCode,
			RetryAfter: search.ParseRetryAfter(resp.Header.Get("Retry-After"), d.clock),
		}
	case resp.StatusCode < 200 || resp.StatusCode >= 300:
		return nil, fmt.Errorf("newznab: download returned HTTP %d", resp.StatusCode)
	}

	body, err := readCapped(resp.Body, maxNZBBytes)
	if err != nil {
		return nil, sanitizeGrabError(err)
	}
	return &search.GrabResult{
		Body:        body,
		ContentType: nzbContentType,
	}, nil
}

// fetch issues a plain GET for an .nzb download URL. The URL already carries the apikey in
// its query, so no auth header is needed. The transport error from Do is a *url.Error whose
// Error() embeds the FULL unredacted URL, so it is routed through apphttp.RedactURLError and
// wrapped under errDownloadRequestFailed: the surfaced cause carries only the scheme://host
// (path/query dropped), so the apikey cannot re-leak through %w regardless of who calls
// fetch(). Context cancellation/deadline sentinels are preserved so normal cancellation stays
// detectable. The caller owns the returned body and interprets the status.
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

// sanitizeGrabError classifies a grab error for surfacing. Sentinels that carry no URL and
// that callers need to classify are passed through: auth and rate-limit (for health), context
// cancellation/deadline, and the size-cap error. An already-enriched errDownloadRequestFailed
// (fetch's HOST-ONLY transport failure) is passed through verbatim so its scheme://host cause
// is not collapsed or double-prefixed. Anything else (e.g. a readCapped io error, which is
// already routed through apphttp.RedactError) is flattened to the bare errDownloadRequestFailed
// sentinel rather than risk surfacing a URL.
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

// readCapped reads up to limit bytes, returning errDownloadTooLarge when the source exceeds
// the cap rather than silently truncating (a truncated .nzb is corrupt). The returned errors
// never carry the source URL. A transport read error is scrubbed through apphttp.RedactError
// in case it echoes the apikey-bearing URL.
func readCapped(r io.Reader, limit int64) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, fmt.Errorf("newznab: read download response: %s", apphttp.RedactError(err))
	}
	if int64(len(body)) > limit {
		return nil, errDownloadTooLarge
	}
	return body, nil
}
