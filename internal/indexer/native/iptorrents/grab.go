package iptorrents

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

// maxTorrentBytes caps a fetched .torrent. It is far larger than the search/test HTML
// cap because a large pack carries megabytes of piece hashes; readCapped errors rather
// than silently truncating a corrupt torrent.
const maxTorrentBytes = 64 << 20

var errDownloadTooLarge = errors.New("iptorrents: download exceeds the size cap")

// Grab fetches the resolved IPTorrents download URL with the session cookie + User-Agent
// and returns the .torrent bytes. *arr cannot send that cookie, which is why
// NeedsResolver is true and the served feed routes the download through /dl; this is the
// server-side fetch /dl drives, so neither the cookie nor the download URL reaches the
// feed. The download is a direct torrent (never a magnet), so Redirect is empty. No
// error carries the download link's secret path/query (only its scheme://host can), and
// the bytes go to /dl, never a log.
func (d *driver) Grab(ctx context.Context, link string) (*search.GrabResult, error) {
	resp, err := d.get(ctx, link, "")
	if err != nil {
		return nil, sanitizeGrabError(err)
	}
	defer func() { _ = resp.Body.Close() }()

	switch {
	case search.IsRateLimitStatus(resp.StatusCode):
		return nil, &search.RateLimitedError{StatusCode: resp.StatusCode, RetryAfter: d.parseRetryAfter(resp)}
	case resp.StatusCode == stdhttp.StatusUnauthorized || resp.StatusCode == stdhttp.StatusForbidden:
		return nil, fmt.Errorf("iptorrents: download unauthorized: %w", login.ErrLoginFailed)
	case resp.StatusCode < 200 || resp.StatusCode >= 300:
		return nil, fmt.Errorf("iptorrents: download returned HTTP %d", resp.StatusCode)
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

// sanitizeGrabError keeps the sentinels the registry classifies on (auth, rate-limit) and
// routes every other fetch error through a host-only fallback. The fallback %w-wraps the
// cause, which is host-only — either get()'s transport error (host-only by construction)
// or an io read error (URL-free) — and RedactURLError additionally rebuilds a stray
// build-request *url.Error host-only, so the download link's secret path/query never
// surfaces (only its scheme://host can).
func sanitizeGrabError(err error) error {
	if errors.Is(err, login.ErrLoginFailed) {
		return err
	}
	var rl *search.RateLimitedError
	if errors.As(err, &rl) {
		return err
	}
	return fmt.Errorf("iptorrents: download request failed: %w", apphttp.RedactURLError(err))
}

// readCapped reads up to limit bytes, returning errDownloadTooLarge when the source
// exceeds the cap rather than silently truncating (a truncated .torrent is corrupt).
// The returned errors never carry the source URL.
func readCapped(r io.Reader, limit int64) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, fmt.Errorf("iptorrents: read download response: %w", err)
	}
	if int64(len(body)) > limit {
		return nil, errDownloadTooLarge
	}
	return body, nil
}
