package iptorrents

import (
	"context"
	"fmt"
	stdhttp "net/http"
	"strings"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/login"
	"github.com/autobrr/harbrr/internal/indexer/native"
)

const (
	// loggedInMarker is the logout link Prowlarr's CheckIfLoginNeeded looks for to
	// confirm the cookie still authenticates; its absence is an auth failure.
	loggedInMarker = "lout.php"
)

// get issues a GET carrying the session cookie and User-Agent headers. The cookie is
// a header (never the URL), so the URL carries no secret; a transport error still
// surfaces only its scheme://host through native.Base. accept sets the Accept header
// when non-empty (the search wants HTML; a torrent download must not force a content
// type).
func (d *driver) get(ctx context.Context, rawurl, accept string, download bool) (*native.Response, error) {
	req, err := stdhttp.NewRequestWithContext(ctx, stdhttp.MethodGet, rawurl, nil)
	if err != nil {
		return nil, fmt.Errorf("iptorrents: build request: %w", err)
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
	if download {
		return d.DoDownload(ctx, req, native.ClassifyAuth403)
	}
	return d.Do(ctx, req, native.ClassifyAuth403)
}

// Test verifies the configured cookie still authenticates (the management
// "test indexer" action). It fetches the torrent list page and, mirroring Prowlarr's
// CheckIfLoginNeeded, treats the absence of the logout link (lout.php) as an auth
// failure wrapped with login.ErrLoginFailed (so the registry records an auth_failure
// health event).
func (d *driver) Test(ctx context.Context) error {
	resp, err := d.get(ctx, d.BaseURL+searchPath, "text/html", false)
	if err != nil {
		return err
	}
	if !strings.Contains(string(resp.Body), loggedInMarker) {
		return fmt.Errorf("iptorrents: cookie authentication failed: %w", login.ErrLoginFailed)
	}
	return nil
}
