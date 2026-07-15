package torrentday

import (
	"context"
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
// a transport error still surfaces only its scheme://host through native.Base, then
// Base.ScrubErr value-scrubs any echoed cookie/user_agent while preserving the
// wrapped sentinel (e.g. login.ErrLoginFailed) through the scrub. cookie is
// IsSecret-classified (its name carries the "cookie" token) so it comes from
// Base.Scrub's derived set; user_agent carries no credential token and is not a
// declared setting at all, so it is passed as an explicit extra. accept sets the
// Accept header when non-empty (the search wants JSON; a torrent download must not
// force a content type). The cookie, headers, and body are never logged.
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
	return resp, d.ScrubErr(err, strings.TrimSpace(d.Cfg["user_agent"]))
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
