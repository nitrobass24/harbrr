package hdbits

import (
	"bytes"
	"context"
	"fmt"
	stdhttp "net/http"

	apphttp "github.com/autobrr/harbrr/internal/http"
)

// maxBodyBytes caps an api/torrents JSON response. An HDBits page is small JSON (a
// {status,message,data[]} envelope with up to limit=100 rows), so this is generous while
// still bounding a hostile or runaway body.
const maxBodyBytes = 8 << 20 // 8 MiB

// searchPath is the HDBits JSON search endpoint (Prowlarr: "{BaseUrl}/api/torrents",
// HttpMethod.Post). The username and passkey ride as top-level fields inside the POST
// body, never the URL.
const searchPath = "api/torrents"

// post issues the JSON POST to the api/torrents endpoint. The body carries the username
// and passkey as top-level fields, so it is secret-bearing and never logged; a transport
// error routes the URL (never the body) through apphttp.RedactURL. Content-Type and
// Accept are application/json (Prowlarr sets both). The caller owns the returned body and
// interprets the status.
func (d *driver) post(ctx context.Context, body []byte) (*stdhttp.Response, error) {
	rawurl := d.baseURL + searchPath
	req, err := stdhttp.NewRequestWithContext(ctx, stdhttp.MethodPost, rawurl, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("hdbits: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := d.doer.Do(req)
	if err != nil {
		return nil, fmt.Errorf("hdbits: request to %s: %w", apphttp.RedactURL(rawurl), err)
	}
	return resp, nil
}
