package gazelle

import (
	"context"
	"errors"
	"fmt"
	"io"
	stdhttp "net/http"
	"strings"

	apphttp "github.com/autobrr/harbrr/internal/http"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/login"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
)

// maxTorrentBytes caps a fetched .torrent. It is far larger than the browse JSON cap
// because a large box-set carries megabytes of piece hashes; readTorrent errors rather
// than silently truncating a corrupt torrent.
const maxTorrentBytes = 64 << 20

// usetokenParam is the query suffix that requests a freeleech token on a download. The
// freeleech fallback strips it (its presence is also the trigger condition for the
// fallback, matching Prowlarr's link.Query.Contains("usetoken=1") guard).
const usetokenParam = "&usetoken=1"

var errDownloadTooLarge = errors.New("gazelle: download exceeds the size cap")

// Grab fetches the header-authenticated download URL server-side and returns the
// .torrent bytes. The link itself carries no secret (the API key rides in the
// Authorization header, added by get); the served feed therefore exposes the link and
// routes the fetch through the /dl proxy, which is what this server-side Grab drives.
//
// Freeleech-token fallback (Prowlarr's Redacted/Orpheus Download override): when the
// freeleech-token setting is on and the link requested a token (usetoken=1) but the
// response body is not a bencoded torrent (first byte != 'd'), the site returned an HTML
// "no tokens left" page instead of a torrent — so retry the SAME id with usetoken
// stripped. OPS never sees usetoken=0 because the retry removes the param entirely.
func (d *driver) Grab(ctx context.Context, link string) (*search.GrabResult, error) {
	body, contentType, err := d.fetchTorrent(ctx, link)
	if err != nil {
		return nil, err
	}
	if d.useFreeleechToken() && isTokenRequest(link) && !isBencoded(body) {
		retryLink := strings.Replace(link, usetokenParam, "", 1)
		body, contentType, err = d.fetchTorrent(ctx, retryLink)
		if err != nil {
			return nil, err
		}
	}
	return &search.GrabResult{Body: body, ContentType: contentType}, nil
}

// fetchTorrent GETs one download URL and returns its body and Content-Type. It maps a
// 401/403 to login.ErrLoginFailed, a rate-limit status to a RateLimitedError, and any
// other non-2xx to an error; transport and read errors pass through sanitizeGrabError,
// which surfaces at most the host and never the download link or a credential.
func (d *driver) fetchTorrent(ctx context.Context, link string) ([]byte, string, error) {
	resp, err := d.get(ctx, link)
	if err != nil {
		return nil, "", sanitizeGrabError(err)
	}
	defer func() { _ = resp.Body.Close() }()

	switch {
	case resp.StatusCode == stdhttp.StatusUnauthorized || resp.StatusCode == stdhttp.StatusForbidden:
		return nil, "", fmt.Errorf("gazelle: download unauthorized: %w", login.ErrLoginFailed)
	case search.IsRateLimitStatus(resp.StatusCode):
		return nil, "", &search.RateLimitedError{
			StatusCode: resp.StatusCode,
			RetryAfter: search.ParseRetryAfter(resp.Header.Get("Retry-After"), d.clock),
		}
	case resp.StatusCode < 200 || resp.StatusCode >= 300:
		return nil, "", fmt.Errorf("gazelle: download returned HTTP %d", resp.StatusCode)
	}

	body, err := readTorrent(resp.Body, maxTorrentBytes)
	if err != nil {
		return nil, "", sanitizeGrabError(err)
	}
	return body, resp.Header.Get("Content-Type"), nil
}

// Test exercises the credentials with an empty browse query: a 401/403 surfaces as
// login.ErrLoginFailed (the registry records an auth_failure health event), while a
// parseable empty page confirms the key works.
func (d *driver) Test(ctx context.Context) error {
	_, err := d.Search(ctx, search.Query{})
	return err
}

// isTokenRequest reports whether a download link requested a freeleech token, mirroring
// Prowlarr's link.Query.Contains("usetoken=1") guard for the fallback.
func isTokenRequest(link string) bool {
	return strings.Contains(link, usetokenParam)
}

// isBencoded reports whether body looks like a bencoded .torrent (a bencoded dict starts
// with 'd'). An HTML "no tokens left" page does not, which is the freeleech fallback's
// signal. An empty body is treated as not bencoded.
func isBencoded(body []byte) bool {
	return len(body) > 0 && body[0] == 'd'
}

// sanitizeGrabError classifies a grab error. Sentinels callers need to classify pass
// through unchanged: auth and rate-limit (for health), context cancellation/deadline (so
// a cancelled request is not misreported as a failure), and the size-cap error. Every
// other error is %w-wrapped in the generic failure message so the cause surfaces for
// diagnosis.
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
	// The fallback %w-wraps the cause, which is host-only: either get()'s transport error
	// (host-only by construction — apphttp.SchemeHost + RedactURLError) or readTorrent's io
	// read error (URL-free). RedactURLError additionally rebuilds a stray build-request
	// *url.Error host-only, so the download link's secret path/query never surfaces — only
	// its scheme://host can.
	return fmt.Errorf("gazelle: download request failed: %w", apphttp.RedactURLError(err))
}

// readTorrent reads up to limit bytes, returning errDownloadTooLarge when the source
// exceeds the cap rather than silently truncating (a truncated .torrent is corrupt). The
// returned errors never carry the source URL.
func readTorrent(r io.Reader, limit int64) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, fmt.Errorf("gazelle: read download response: %w", err)
	}
	if int64(len(body)) > limit {
		return nil, errDownloadTooLarge
	}
	return body, nil
}
