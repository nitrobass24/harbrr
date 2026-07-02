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
// secret-bearing: it is NEVER logged, and a transport error redacts it through
// apphttp.RedactURL (which strips the torrent_pass value) before the URL reaches the
// wrapped error. accept sets the Accept header — "application/json" for a scrape.php
// query, empty for a .torrent download so JSON is not forced on binary bytes. The caller
// owns the returned body and interprets the status.
func (d *driver) get(ctx context.Context, rawurl, accept string) (*stdhttp.Response, error) {
	if d.doer == nil {
		return nil, errors.New("animebytes: nil request doer")
	}
	req, err := stdhttp.NewRequestWithContext(ctx, stdhttp.MethodGet, rawurl, nil)
	if err != nil {
		// rawurl AND the inner *url.Error both carry the passkey (url.Error.Error
		// prints the full URL), so neither may be wrapped with %w. Surface only the
		// redacted URL plus a generic cause.
		return nil, fmt.Errorf("animebytes: build request to %s: invalid url", apphttp.RedactURL(rawurl))
	}
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	resp, err := d.doer.Do(req)
	if err != nil {
		// The *url.Error from Do stringifies the full request URL (passkey included),
		// so it must not be wrapped with %w. Context cancellation/deadline carry no URL
		// and callers (Grab health, Search) need to classify them, so pass those
		// sentinels through unwrapped; otherwise surface only the redacted URL.
		switch {
		case errors.Is(err, context.Canceled):
			return nil, context.Canceled
		case errors.Is(err, context.DeadlineExceeded):
			return nil, context.DeadlineExceeded
		}
		return nil, fmt.Errorf("animebytes: request to %s: transport error", apphttp.RedactURL(rawurl))
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
