package myanonamouse

import (
	"context"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
	"github.com/autobrr/harbrr/internal/version"
)

// userAgent identifies harbrr on the download request. MAM refuses download.php with
// a 406 while the same session's JSON searches succeed, and the one shape that
// differs is the client: a search declares Accept, a download declared neither Accept
// nor a User-Agent (so it rode Go's default "Go-http-client/1.1"), which a
// path-scoped content-negotiation filter fits exactly (autobrr/harbrr#465). Naming
// harbrr honestly beats impersonating a browser. An unset build version (a bare
// `go build`, never a release) degrades to the plain product name.
func userAgent() string {
	if version.Version == "" {
		return "harbrr"
	}
	return "harbrr/" + version.Version
}

// Grab fetches the resolved download URL with the mam_id Cookie and returns the
// .torrent bytes. *arr cannot send the Cookie, which is why NeedsResolver is true and
// the served feed routes the download through the /dl proxy; this is the server-side
// fetch /dl drives, so neither the cookie nor any key in the download URL reaches the
// feed. The download is a direct torrent (never a magnet), so Redirect is empty.
// Status classification (MAM dialect), the torrent size cap
// (native.ErrDownloadTooLarge), and host-only transport redaction live in the base
// DoDownload: a grab error surfaces at most the download endpoint's scheme://host
// (never the mam_id, which rides a header, nor a secret in the URL's path/query), and
// the bytes go to /dl, never a log.
func (d *driver) Grab(ctx context.Context, link string) (*search.GrabResult, error) {
	// Accept anything (a .torrent is not JSON) and identify the client — the search
	// path is untouched because it works as-is (see userAgent).
	req, err := d.newRequest(ctx, link, "*/*")
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent())
	resp, err := d.doDownload(ctx, req)
	if err != nil {
		return nil, err
	}
	return &search.GrabResult{
		Body:        resp.Body,
		ContentType: resp.Header.Get("Content-Type"),
	}, nil
}
