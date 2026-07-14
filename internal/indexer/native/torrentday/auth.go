package torrentday

import (
	"context"
	"errors"
	"fmt"
	stdhttp "net/http"
	"strings"

	"github.com/autobrr/harbrr/internal/indexer/native"
)

const (
	// loginRedirectMarker is the path TorrentDay redirects an unauthenticated request
	// to (Prowlarr throws on a redirect whose RedirectUrl contains /login.php); its
	// presence in a 3xx Location is an auth failure.
	loginRedirectMarker = "/login.php"
)

// get issues a GET carrying the session cookie (and User-Agent when configured) as
// headers. The cookie is a header, never the URL, so the served URL carries no secret;
// a transport error still surfaces only its scheme://host through native.Base. accept
// sets the Accept header when non-empty (the search wants JSON; a torrent download must
// not force a content type). The cookie, headers, and body are never logged.
func (d *driver) get(ctx context.Context, rawurl, accept string, download bool) (*native.Response, error) {
	req, err := stdhttp.NewRequestWithContext(ctx, stdhttp.MethodGet, rawurl, nil)
	if err != nil {
		return nil, fmt.Errorf("torrentday: build request: %w", err)
	}
	if cookie := strings.TrimSpace(d.Cfg["cookie"]); cookie != "" {
		req.Header.Set("Cookie", cookie)
	}
	if ua := strings.TrimSpace(d.Cfg["user_agent"]); ua != "" {
		req.Header.Set("User-Agent", ua)
	}
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	var resp *native.Response
	if download {
		resp, err = d.DoDownload(ctx, req, native.ClassifyAuth403)
	} else {
		resp, err = d.Do(ctx, req, native.ClassifyAuth403)
	}
	return resp, d.scrubError(err)
}

// isLoginRedirect reports whether resp is a 3xx redirect whose Location points at the
// login page. TorrentDay redirects an unauthenticated (stale-cookie) request to
// /login.php instead of returning a 401/403, so a redirect to that path is an auth
// failure (mirroring Prowlarr's HasHttpRedirect + RedirectUrl check).
func isLoginRedirect(resp *native.Response) bool {
	if resp.StatusCode < 300 || resp.StatusCode >= 400 {
		return false
	}
	return strings.Contains(resp.Header.Get("Location"), loginRedirectMarker)
}

func (d *driver) scrubError(err error) error {
	if err == nil {
		return nil
	}
	msg := scrubSecrets(err.Error(), d.Cfg)
	if msg == err.Error() {
		return err
	}
	return errors.New(msg)
}

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
