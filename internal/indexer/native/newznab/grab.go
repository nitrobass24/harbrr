package newznab

import (
	"context"
	"errors"
	"fmt"
	stdhttp "net/http"

	apphttp "github.com/autobrr/harbrr/internal/http"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/login"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
	"github.com/autobrr/harbrr/internal/indexer/native"
)

// nzbContentType is what the /dl proxy serves a fetched .nzb as. harbrr's torrent
// content-type constant (search.torrentContentType) is torrent-specific, so the Newznab
// driver sets its own.
const nzbContentType = "application/x-nzb"

// errDownloadRequestFailed is the transport-failure sentinel. A build-request failure
// returns it bare (there is no URL to leak). A transport failure from Do wraps it with a
// HOST-ONLY cause (apphttp.RedactURLError drops the apikey-bearing path/query), so the
// scheme://host surfaces for diagnosis while the apikey cannot re-leak through %w.
var errDownloadRequestFailed = errors.New("newznab: download request failed")

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
		if resp != nil {
			return nil, err
		}
		return nil, sanitizeGrabError(err)
	}
	return &search.GrabResult{
		Body:        resp.Body,
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
func (d *driver) fetch(ctx context.Context, rawurl string) (*native.Response, error) {
	req, err := stdhttp.NewRequestWithContext(ctx, stdhttp.MethodGet, rawurl, nil)
	if err != nil {
		return nil, errDownloadRequestFailed
	}
	resp, err := d.DoDownload(ctx, req, native.ClassifyRateLimit403)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return nil, context.Canceled
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, context.DeadlineExceeded
		}
		if resp != nil {
			return resp, err
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
		errors.Is(err, native.ErrDownloadTooLarge):
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
