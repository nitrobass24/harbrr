package animebytes

import (
	"context"
	"errors"
	"fmt"
	"io"
	stdhttp "net/http"

	apphttp "github.com/autobrr/harbrr/internal/http"
)

// maxBodyBytes caps a scrape.php JSON response (search/error). It is generous — an
// AnimeBytes page is small JSON — but bounds a hostile or runaway body.
const maxBodyBytes = 8 << 20 // 8 MiB

// get issues an authenticated GET against an AnimeBytes URL. AnimeBytes carries both the
// username and the passkey (torrent_pass) in the request, so the URL itself is
// secret-bearing: it is NEVER logged, and both a build-request and a transport error
// surface only its scheme://host (apphttp.SchemeHost / apphttp.RedactURLError, which drop
// the path and query where the passkey lives) before the URL reaches the wrapped error.
// accept sets the Accept header — "application/json" for a scrape.php query, empty for a
// .torrent download so JSON is not forced on binary bytes. The caller owns the returned
// body and interprets the status.
func (d *driver) get(ctx context.Context, rawurl, accept string) (*stdhttp.Response, error) {
	if d.doer == nil {
		return nil, errors.New("animebytes: nil request doer")
	}
	req, err := stdhttp.NewRequestWithContext(ctx, stdhttp.MethodGet, rawurl, nil)
	if err != nil {
		// The build error is a *url.Error quoting the full (passkey-bearing) URL, so it
		// is routed through apphttp.RedactURLError, which rebuilds it host-only.
		return nil, fmt.Errorf("animebytes: build request: %w", apphttp.RedactURLError(err))
	}
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	resp, err := d.doer.Do(req)
	if err != nil {
		// The transport error may be a *url.Error quoting the full (passkey-bearing)
		// URL; SchemeHost surfaces only scheme://host and RedactURLError rebuilds the
		// cause host-only. %w preserves context.Canceled/DeadlineExceeded in the chain,
		// so callers (Grab health, Search) still classify them via errors.Is.
		return nil, fmt.Errorf("animebytes: request to %s: %w", apphttp.SchemeHost(rawurl), apphttp.RedactURLError(err))
	}
	return resp, nil
}

// readBody reads a capped, scrubbed response body. The passkey is scrubbed from any
// error message a read failure produces — not the body itself (a torrent body is binary
// and a JSON body is parsed downstream) — so a server that echoes the submitted passkey
// in a transport-layer error never leaks it.
func (d *driver) readBody(resp *stdhttp.Response) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		return nil, fmt.Errorf("animebytes: read response: %s", d.scrubPasskey(err.Error()))
	}
	return body, nil
}
