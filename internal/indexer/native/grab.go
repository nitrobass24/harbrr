package native

import (
	"context"
	"errors"
	"fmt"
	stdhttp "net/http"

	apphttp "github.com/autobrr/harbrr/internal/http"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/login"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
)

// ErrGrabRequestFailed is GrabDirect's build-request-failure sentinel. A request that
// cannot even be built may quote the credential-bearing download URL in its cause, so it
// is returned bare — never wrapped around the underlying error. It is intentionally
// family-generic (unlike the usenet pair's own errDownloadRequestFailed, passed into
// GrabNZB): no URL leaks either way, and every GrabDirect caller shares one sentinel.
var ErrGrabRequestFailed = errors.New("native: download request failed")

// GrabDirect is the shared direct-GET grab path for a driver whose download link is
// already URL-credentialed (a passkey/authkey/rsskey riding the path or query) and needs
// no extra auth header: build a plain GET, run it through DoDownload under the caller's
// classify dialect, and return the body/Content-Type as a GrabResult. classify is the
// endpoint's status dialect (beyondhd/broadcastthenet: ClassifyAuth403; hdbits:
// ClassifyRateLimit403) — same "required parameter" posture as Do/DoDownload. Transport
// redaction, status classification, and the size cap all live in DoDownload; GrabDirect
// adds no error handling beyond it, so the driver's returned error is DoDownload's
// unchanged (never carries the credential-bearing link).
func (b *Base) GrabDirect(ctx context.Context, link string, classify Classify) (*search.GrabResult, error) {
	req, err := stdhttp.NewRequestWithContext(ctx, stdhttp.MethodGet, link, nil)
	if err != nil {
		return nil, ErrGrabRequestFailed
	}
	resp, err := b.DoDownload(ctx, req, classify)
	if err != nil {
		return nil, err
	}
	return &search.GrabResult{
		Body:        resp.Body,
		ContentType: resp.Header.Get("Content-Type"),
	}, nil
}

// GrabNZB is the shared usenet grab path for newznab and nzbindex: a plain GET for a
// .nzb download URL (the apikey, if any, already rides the query) under DoDownload,
// classified per classify, with the result sanitized so no build/transport error can
// surface the download URL. errDownloadRequestFailed is the caller's OWN family-prefixed
// sentinel (e.g. "newznab: download request failed") — kept as a caller parameter rather
// than hoisted, because the two packages' tests assert on its exact family-prefixed
// message and errors.Is identity.
//
// A classified-status error (login.ErrLoginFailed, *search.RateLimitedError) is returned
// as-is so health classification survives; a context cancellation/deadline is preserved;
// anything else collapses through sanitizeGrabError so a URL can never leak through an
// unanticipated error shape.
func (b *Base) GrabNZB(ctx context.Context, link, contentType string, classify Classify, errDownloadRequestFailed error) (*search.GrabResult, error) {
	req, err := stdhttp.NewRequestWithContext(ctx, stdhttp.MethodGet, link, nil)
	if err != nil {
		return nil, errDownloadRequestFailed
	}
	resp, err := b.DoDownload(ctx, req, classify)
	if err != nil {
		switch {
		case errors.Is(err, context.Canceled):
			return nil, context.Canceled
		case errors.Is(err, context.DeadlineExceeded):
			return nil, context.DeadlineExceeded
		case resp != nil:
			// A classified-status error (login.ErrLoginFailed / RateLimitedError):
			// pass through unsanitized so callers keep their classification.
			return nil, err
		}
		return nil, sanitizeGrabError(err, errDownloadRequestFailed)
	}
	return &search.GrabResult{Body: resp.Body, ContentType: contentType}, nil
}

// sanitizeGrabError classifies GrabNZB's RAW DoDownload error for surfacing. Sentinels
// that carry no URL and that callers need to classify are passed through: auth and
// rate-limit (for health), context cancellation/deadline, and the size-cap error. A
// transport failure roundTrip marked host-redacted (its cause is PROVABLY scrubbed to
// scheme://host) keeps its detail, wrapped as errDownloadRequestFailed. Anything else is
// flattened to the bare errDownloadRequestFailed sentinel: an unmarked error may embed a
// secret-bearing URL in free text that no scrubber can safely rewrite.
func sanitizeGrabError(err, errDownloadRequestFailed error) error {
	switch {
	case errors.Is(err, login.ErrLoginFailed),
		errors.Is(err, context.Canceled),
		errors.Is(err, context.DeadlineExceeded),
		errors.Is(err, ErrDownloadTooLarge):
		return err
	}
	var rl *search.RateLimitedError
	if errors.As(err, &rl) {
		return err
	}
	if apphttp.IsHostRedacted(err) {
		return fmt.Errorf("%w: %w", errDownloadRequestFailed, err)
	}
	return errDownloadRequestFailed
}

// NormalizeReadError keeps the pre-Base health sentinel for a mid-body API read failure
// (Do's io.ReadAll after the status is already 2xx) while leaving transport/status errors
// in Base's native form. Shared verbatim by newznab and nzbindex, whose per-package copies
// were byte-identical. Classifies via errors.Is(err, ErrBodyRead) rather than matching
// roundTrip's assembled error text, so rewording that text can't silently break it.
func NormalizeReadError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, ErrBodyRead) {
		return fmt.Errorf("%w: %w", err, search.ErrParseError)
	}
	return err
}
