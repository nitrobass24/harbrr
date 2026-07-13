package gazelle

import (
	"context"
	"strings"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
	"github.com/autobrr/harbrr/internal/indexer/native"
)

// usetokenParam is the query suffix that requests a freeleech token on a download. The
// freeleech fallback strips it (its presence is also the trigger condition for the
// fallback, matching Prowlarr's link.Query.Contains("usetoken=1") guard).
const usetokenParam = "&usetoken=1"

// Grab fetches the header-authenticated download URL server-side and returns the
// .torrent bytes. The link itself carries no secret (the API key rides in the
// Authorization header, added by newRequest); the served feed therefore exposes the
// link and routes the fetch through the /dl proxy, which is what this server-side Grab
// drives.
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

// fetchTorrent GETs one download URL and returns its body and Content-Type. Status
// classification (401/403 -> login.ErrLoginFailed, rate-limit -> RateLimitedError),
// the torrent size cap (native.ErrDownloadTooLarge rather than a silent truncation),
// and host-only transport redaction all live in the base DoDownload, so a grab error
// surfaces at most the endpoint's host and never the download link or a credential.
func (d *driver) fetchTorrent(ctx context.Context, link string) ([]byte, string, error) {
	req, err := d.newRequest(ctx, link)
	if err != nil {
		return nil, "", err
	}
	resp, err := d.DoDownload(ctx, req, native.ClassifyAuth403)
	if err != nil {
		return nil, "", err
	}
	return resp.Body, resp.Header.Get("Content-Type"), nil
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
