package torrentday

import (
	"context"
	"fmt"
	stdhttp "net/http"
	"strings"
	"time"

	apphttp "github.com/autobrr/harbrr/internal/http"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
)

const (
	// maxBodyBytes caps a fetched /t.json search/test response. It is a small JSON
	// array, far smaller than a .torrent, so the search cap is modest.
	maxBodyBytes = 8 << 20 // 8 MiB
	// loginRedirectMarker is the path TorrentDay redirects an unauthenticated request
	// to (Prowlarr throws on a redirect whose RedirectUrl contains /login.php); its
	// presence in a 3xx Location is an auth failure.
	loginRedirectMarker = "/login.php"
)

// get issues a GET carrying the session cookie (and User-Agent when configured) as
// headers. The cookie is a header, never the URL, so the served URL carries no secret;
// a transport error still redacts the URL. The caller owns the returned body and
// interprets the status. accept sets the Accept header when non-empty (the search wants
// JSON; a torrent download must not force a content type). The cookie, headers, and
// body are never logged.
func (d *driver) get(ctx context.Context, rawurl, accept string) (*stdhttp.Response, error) {
	req, err := stdhttp.NewRequestWithContext(ctx, stdhttp.MethodGet, rawurl, nil)
	if err != nil {
		return nil, fmt.Errorf("torrentday: build request: %w", err)
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
		// The cookie rides only the request header, never the URL, so the redacted URL
		// and the transport error carry no session secret; scrubSecrets is a final
		// belt-and-suspenders pass, and the wrapped err keeps the chain intact for
		// context.Canceled / DeadlineExceeded callers.
		return nil, &scrubbedError{
			msg: scrubSecrets(fmt.Sprintf("torrentday: request to %s: %s", apphttp.RedactURL(rawurl), err), d.cfg),
			err: err,
		}
	}
	return resp, nil
}

// isLoginRedirect reports whether resp is a 3xx redirect whose Location points at the
// login page. TorrentDay redirects an unauthenticated (stale-cookie) request to
// /login.php instead of returning a 401/403, so a redirect to that path is an auth
// failure (mirroring Prowlarr's HasHttpRedirect + RedirectUrl check).
func isLoginRedirect(resp *stdhttp.Response) bool {
	if resp.StatusCode < 300 || resp.StatusCode >= 400 {
		return false
	}
	return strings.Contains(resp.Header.Get("Location"), loginRedirectMarker)
}

// parseRetryAfter is a thin wrapper so Search/Grab share the driver clock without
// repeating the header lookup.
func (d *driver) parseRetryAfter(resp *stdhttp.Response) time.Duration {
	return search.ParseRetryAfter(resp.Header.Get("Retry-After"), d.clock)
}

// scrubbedError carries a secret-scrubbed message while preserving the underlying
// transport error chain (so errors.Is for context.Canceled / DeadlineExceeded still
// works). Its Error() never echoes the session cookie.
type scrubbedError struct {
	msg string
	err error
}

func (e *scrubbedError) Error() string { return e.msg }
func (e *scrubbedError) Unwrap() error { return e.err }

// scrubSecrets removes the configured session cookie (and User-Agent) from a string so
// a wrapped transport error can never leak the secret. The cookie rides only in the
// request header; should a redirect or transport error ever echo it into a message, it
// is replaced with a fixed placeholder.
func scrubSecrets(s string, cfg map[string]string) string {
	out := s
	if cookie := strings.TrimSpace(cfg["cookie"]); cookie != "" {
		out = strings.ReplaceAll(out, cookie, "[REDACTED-COOKIE]")
	}
	if ua := strings.TrimSpace(cfg["user_agent"]); ua != "" {
		out = strings.ReplaceAll(out, ua, "[REDACTED-UA]")
	}
	return out
}
