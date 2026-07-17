package gazelle

import (
	"context"
	"strings"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
)

// usetokenParam is the query suffix that requests a freeleech token on a download. The
// freeleech fallback strips it (its presence is also the trigger condition for the
// fallback, matching Prowlarr's link.Query.Contains("usetoken=1") guard).
const usetokenParam = "&usetoken=1"

// Grab fetches the authenticated download URL server-side and returns the .torrent
// bytes. The link itself carries no secret (fetchTorrent adds the API-key header or
// session cookie); the served feed therefore exposes the link and routes the fetch
// through the /dl proxy, which is what this server-side Grab drives.
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

// fetchResult is fetchTorrent's sessionRetry payload: a torrent body and its
// Content-Type, retried as a pair so a renewed session refetches both together.
type fetchResult struct {
	body        []byte
	contentType string
}

// fetchTorrent fetches one download URL server-side through sessionRetry: the site's
// strategy attaches auth on every attempt (newRequest), and an auth-classified failure
// (redirect/401/403) gets exactly one recovery-and-retry via the strategy before
// surfacing. The base DoDownload owns the torrent size cap (native.ErrDownloadTooLarge
// rather than a silent truncation), status classification, and host-only redaction.
func (d *driver) fetchTorrent(ctx context.Context, link string) ([]byte, string, error) {
	res, err := sessionRetry(ctx, d, "download", func(ctx context.Context) (fetchResult, error) {
		req, session, err := d.newRequest(ctx, link)
		if err != nil {
			return fetchResult{}, err
		}
		resp, err := d.DoDownload(d.requestContext(ctx), req, d.site.classify)
		if err != nil {
			// Tag the auth failure with the request-used generation so Recover
			// renews the right session rather than coalescing against a stale one.
			return fetchResult{}, withGeneration(err, session.generation)
		}
		return fetchResult{body: resp.Body, contentType: resp.Header.Get("Content-Type")}, nil
	})
	return res.body, res.contentType, err
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
