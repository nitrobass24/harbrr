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
	return GrabResultFrom(resp), nil
}

// GrabResultFrom builds the standard grab result from a driver response: the body plus
// the response's Content-Type. Every torrent driver's grab tail is this shape, so it is
// written once here rather than ten times. It is exported because the drivers live in
// sub-packages of native.
//
// This shares only the tail: a driver still runs its own d.get, which is where its
// per-driver auth headers (Bearer, session cookie, Basic, X-API-Key) are applied.
// Routing those drivers through GrabDirect instead would drop the headers.
func GrabResultFrom(resp *Response) *search.GrabResult {
	return &search.GrabResult{Body: resp.Body, ContentType: resp.Header.Get("Content-Type")}
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
// anything else collapses through SanitizeGrabError so a URL can never leak through an
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
		return nil, SanitizeGrabError(err, errDownloadRequestFailed)
	}
	return &search.GrabResult{Body: resp.Body, ContentType: contentType}, nil
}

// SanitizeGrabError classifies a RAW DoDownload error for surfacing. The
// classification callers need always survives — auth and rate-limit (for health),
// context cancellation/deadline, and the size-cap error — but only BARE: the wrapper's
// free text is dropped unless roundTrip marked the error host-redacted (its cause is
// PROVABLY scrubbed to scheme://host), in which case the full detail is kept. The
// rate-limit case returns the typed *search.RateLimitedError, so RetryAfter survives the
// collapse. Anything not classified is the errDownloadRequestFailed sentinel — wrapped
// around the error when host-redacted, bare otherwise, because an unmarked error may
// embed a secret-bearing URL in free text that no scrubber can safely rewrite.
//
// errDownloadRequestFailed is the caller's own family-prefixed sentinel, so this is
// shared by GrabNZB and by any driver (avistaz) whose Grab sanitizes its own error.
func SanitizeGrabError(err, errDownloadRequestFailed error) error {
	redacted := apphttp.IsHostRedacted(err)
	for _, sentinel := range []error{login.ErrLoginFailed, context.Canceled, context.DeadlineExceeded, ErrDownloadTooLarge} {
		if errors.Is(err, sentinel) {
			if redacted {
				return err
			}
			return sentinel
		}
	}
	var rl *search.RateLimitedError
	if errors.As(err, &rl) {
		if redacted {
			return err
		}
		return rl
	}
	if redacted {
		return fmt.Errorf("%w: %w", errDownloadRequestFailed, err)
	}
	return errDownloadRequestFailed
}
