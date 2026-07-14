package torrentday

import (
	"context"
	"fmt"

	apphttp "github.com/autobrr/harbrr/internal/http"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/login"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
)

// Grab fetches the resolved TorrentDay download URL (download.php/<id>/<id>.torrent) with
// the session cookie and returns the .torrent bytes. *arr cannot send that cookie, which
// is why NeedsResolver is true and the served feed routes the download through /dl; this
// is the server-side fetch /dl drives, so neither the cookie nor the download URL reaches
// the feed. The download is a direct torrent (never a magnet), so Redirect is empty. A
// transport error surfaces only host-only details and never the cookie, and the bytes go
// to /dl, never a log. The context is stamped WithNoRedirectFollow so a stale-cookie
// redirect to /login.php surfaces as an auth failure (isLoginRedirect) rather than being
// followed.
func (d *driver) Grab(ctx context.Context, link string) (*search.GrabResult, error) {
	resp, err := d.get(apphttp.WithNoRedirectFollow(ctx), link, "", true)
	if err != nil {
		if resp != nil && isLoginRedirect(resp) {
			return nil, fmt.Errorf("torrentday: download redirected to login: %w", login.ErrLoginFailed)
		}
		return nil, err
	}
	return &search.GrabResult{
		Body:        resp.Body,
		ContentType: resp.Header.Get("Content-Type"),
	}, nil
}
