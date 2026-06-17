package iptorrents

import (
	"context"
	"fmt"
	"io"
	stdhttp "net/http"
	"strings"
	"time"

	apphttp "github.com/autobrr/harbrr/internal/http"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/login"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
)

const (
	// maxBodyBytes caps a fetched search/test page. IPTorrents pages are HTML lists,
	// far smaller than a .torrent, so the search cap is modest.
	maxBodyBytes = 8 << 20 // 8 MiB
	// loggedInMarker is the logout link Prowlarr's CheckIfLoginNeeded looks for to
	// confirm the cookie still authenticates; its absence is an auth failure.
	loggedInMarker = "lout.php"
)

// get issues a GET carrying the session cookie and User-Agent headers. The cookie is
// a header (never the URL), so the URL carries no secret; errors still redact it. The
// caller owns the returned body and interprets the status. accept sets the Accept
// header when non-empty (the search wants HTML; a torrent download must not force a
// content type).
func (d *driver) get(ctx context.Context, rawurl, accept string) (*stdhttp.Response, error) {
	req, err := stdhttp.NewRequestWithContext(ctx, stdhttp.MethodGet, rawurl, nil)
	if err != nil {
		return nil, fmt.Errorf("iptorrents: build request: %w", err)
	}
	if cookie := strings.TrimSpace(d.cfg["cookie"]); cookie != "" {
		req.Header.Set("Cookie", cookie)
	}
	if ua := strings.TrimSpace(d.cfg["user_agent"]); ua != "" {
		req.Header.Set("User-Agent", ua)
	}
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	resp, err := d.doer.Do(req)
	if err != nil {
		return nil, fmt.Errorf("iptorrents: request to %s: %w", apphttp.RedactURL(rawurl), err)
	}
	return resp, nil
}

// Test verifies the configured cookie still authenticates (the management
// "test indexer" action). It fetches the torrent list page and, mirroring Prowlarr's
// CheckIfLoginNeeded, treats the absence of the logout link (lout.php) as an auth
// failure wrapped with login.ErrLoginFailed (so the registry records an auth_failure
// health event).
func (d *driver) Test(ctx context.Context) error {
	resp, err := d.get(ctx, d.baseURL+searchPath, "text/html")
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	switch {
	case search.IsRateLimitStatus(resp.StatusCode):
		return &search.RateLimitedError{
			StatusCode: resp.StatusCode,
			RetryAfter: search.ParseRetryAfter(resp.Header.Get("Retry-After"), d.clock),
		}
	case resp.StatusCode == stdhttp.StatusUnauthorized || resp.StatusCode == stdhttp.StatusForbidden:
		return fmt.Errorf("iptorrents: test unauthorized: %w", login.ErrLoginFailed)
	case resp.StatusCode < 200 || resp.StatusCode >= 300:
		return fmt.Errorf("iptorrents: test returned HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		return fmt.Errorf("iptorrents: read test response: %w", err)
	}
	if !strings.Contains(string(body), loggedInMarker) {
		return fmt.Errorf("iptorrents: cookie authentication failed: %w", login.ErrLoginFailed)
	}
	return nil
}

// parseRetryAfter is a thin wrapper so Search/Grab share the driver clock without
// repeating the header lookup.
func (d *driver) parseRetryAfter(resp *stdhttp.Response) time.Duration {
	return search.ParseRetryAfter(resp.Header.Get("Retry-After"), d.clock)
}
