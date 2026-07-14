package passthepopcorn

import (
	"context"
	"errors"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
)

// errNotTorrent flags a 2xx response whose body is not bencode (a .torrent always begins
// with 'd', a bencoded dictionary). PTP can answer a download with HTTP 200 yet serve a
// JSON error page (e.g. a query-limit notice), so a non-bencode success is rejected
// rather than handed downstream as a corrupt torrent.
var errNotTorrent = errors.New("passthepopcorn: download response is not a torrent")

// Grab fetches the PTP download URL (torrents.php?action=download&id=<id>) server-side and
// returns the .torrent bytes. The link carries no secret — the ApiUser/ApiKey credentials
// ride in headers, attached by get — so the served feed exposes the link and routes the
// fetch through the /dl proxy, which is the server-side fetch this Grab drives
// (DownloadNeedsAuth is true, NeedsResolver is false; the Gazelle model). The download is a
// direct torrent (never a magnet), so Redirect is empty. A 401 maps to login.ErrLoginFailed;
// a 403 (PTP's query-limit) or a 429/503 maps to a RateLimitedError (the parity target
// raises RequestLimitReachedException on 403 — a transient pacing signal, not bad creds);
// any other non-2xx is an error; transport and read errors go through native.Base so only
// host-only details surface — never a path, query, or credential. The bytes go to /dl,
// never a log.
func (d *driver) Grab(ctx context.Context, link string) (*search.GrabResult, error) {
	resp, err := d.get(ctx, link, "", true)
	if err != nil {
		return nil, err
	}
	// A .torrent is a bencoded dictionary, which always begins with 'd'. PTP can return
	// a 2xx whose body is a JSON error page instead of a torrent; reject that here so a
	// non-torrent never reaches qBittorrent.
	if len(resp.Body) == 0 || resp.Body[0] != 'd' {
		return nil, errNotTorrent
	}
	return &search.GrabResult{
		Body:        resp.Body,
		ContentType: resp.Header.Get("Content-Type"),
	}, nil
}

// Test exercises the credentials with an empty browse query: a 401 surfaces as
// login.ErrLoginFailed (the registry records an auth_failure health event), a 403/429/503
// surfaces as a RateLimitedError, while a parseable response confirms the credentials work.
// Reuses Search so the test path is the real request path, including the status mapping and
// header auth.
func (d *driver) Test(ctx context.Context) error {
	_, err := d.Search(ctx, search.Query{})
	return err
}
