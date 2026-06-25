package animebytes

import (
	"context"
	"fmt"
	"io"
	stdhttp "net/http"

	apphttp "github.com/autobrr/harbrr/internal/http"
)

// maxBodyBytes caps a scrape.php JSON response (search/error). It is generous — an
// AnimeBytes page is small JSON — but bounds a hostile or runaway body.
const maxBodyBytes = 8 << 20 // 8 MiB

// get issues an authenticated GET against a scrape.php URL. AnimeBytes carries both the
// username and the passkey (torrent_pass) in the query, so the URL itself is
// secret-bearing: it is NEVER logged, and a transport error redacts it through
// apphttp.RedactURL (which strips the torrent_pass value) before the URL reaches the
// wrapped error. The Accept header forces JSON. The caller owns the returned body and
// interprets the status.
func (d *driver) get(ctx context.Context, rawurl string) (*stdhttp.Response, error) {
	req, err := stdhttp.NewRequestWithContext(ctx, stdhttp.MethodGet, rawurl, nil)
	if err != nil {
		// rawurl carries the passkey, so redact it before it enters the error.
		return nil, fmt.Errorf("animebytes: build request to %s: %w", apphttp.RedactURL(rawurl), err)
	}
	req.Header.Set("Accept", "application/json")
	resp, err := d.doer.Do(req)
	if err != nil {
		return nil, fmt.Errorf("animebytes: request to %s: %w", apphttp.RedactURL(rawurl), err)
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
