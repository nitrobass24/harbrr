package iptorrents

import (
	"bytes"
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
	req, err := d.NewRequest(ctx, stdhttp.MethodGet, rawurl, nil)
	if err != nil {
		return nil, err
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

// requireLoggedIn mirrors Prowlarr's CheckIfLoginNeeded, which runs on every indexer
// response, not just the test one: an expired cookie is answered with a 200 login page,
// so the absence of the logout link (lout.php) is an auth failure wrapped with
// login.ErrLoginFailed (the registry records an auth_failure health event) rather than
// a silently empty result.
func requireLoggedIn(body []byte) error {
	if bytes.Contains(body, []byte(loggedInMarker)) {
		return nil
	}
	return fmt.Errorf("iptorrents: cookie authentication failed: %w", login.ErrLoginFailed)
}

// Test verifies the configured cookie still authenticates (the management
// "test indexer" action) by fetching the torrent list page and checking the logged-in
// marker.
func (d *driver) Test(ctx context.Context) error {
	resp, err := d.get(ctx, d.BaseURL+searchPath, "text/html", false)
	if err != nil {
		return err
	}
	return requireLoggedIn(resp.Body)
}
