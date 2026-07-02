package beyondhd

import (
	"bytes"
	"context"
	"fmt"
	stdhttp "net/http"

	apphttp "github.com/autobrr/harbrr/internal/http"
)

// maxBodyBytes caps an api/torrents JSON response. A BeyondHD page is small JSON (a
// {status_code,status_message,results[]} envelope with up to PageSize=100 rows), so this
// is generous while still bounding a hostile or runaway body.
const maxBodyBytes = 8 << 20 // 8 MiB

// searchPath is the api/torrents endpoint prefix; the configured api_key is appended as a
// trailing path segment ({base}api/torrents/{api_key}, Prowlarr BeyondHDRequestGenerator).
// The api_key in the path makes the request URL secret-bearing, so it is never logged.
const searchPath = "api/torrents/"

// searchURL builds the secret-bearing search URL: {base}api/torrents/{api_key}. The
// api_key is a URL PATH segment (a BHD quirk), so this string must never reach a log; a
// transport error routes it through apphttp.RedactURL — but note RedactURL only redacts
// query params, not path segments, so the URL is additionally kept out of every error.
func (d *driver) searchURL() string {
	return d.baseURL + searchPath + d.cfg["api_key"]
}

// post issues the JSON POST to api/torrents/{api_key}. The api_key rides in the URL path
// and the rsskey rides inside the body, so neither the URL nor the body is ever logged.
// Content-Type and Accept are application/json (Prowlarr sets both). A transport error
// surfaces with the path-embedded api_key scrubbed (apphttp.RedactError + scrubSecrets);
// the raw URL is never placed in the error. The caller owns the returned body.
func (d *driver) post(ctx context.Context, body []byte) (*stdhttp.Response, error) {
	req, err := stdhttp.NewRequestWithContext(ctx, stdhttp.MethodPost, d.searchURL(), bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("beyondhd: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := d.doer.Do(req)
	if err != nil {
		// The api_key sits in the URL path (RedactURL only redacts query params), so the
		// transport error is scrubbed of both secrets rather than echoing the raw URL.
		return nil, fmt.Errorf("beyondhd: search request failed: %s", d.scrubSecrets(apphttp.RedactError(err)))
	}
	return resp, nil
}
