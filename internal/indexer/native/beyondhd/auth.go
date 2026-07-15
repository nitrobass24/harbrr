package beyondhd

import (
	"bytes"
	"context"
	"fmt"
	stdhttp "net/http"

	apphttp "github.com/autobrr/harbrr/internal/http"
	"github.com/autobrr/harbrr/internal/indexer/native"
)

// searchPath is the api/torrents endpoint prefix; the configured api_key is appended as a
// trailing path segment ({base}api/torrents/{api_key}, Prowlarr BeyondHDRequestGenerator).
// The api_key in the path makes the request URL secret-bearing, so it is never logged.
const searchPath = "api/torrents/"

// searchURL builds the secret-bearing search URL: {base}api/torrents/{api_key}. The
// api_key is a URL PATH segment (a BHD quirk), so this string must never reach a log; a
// transport error routes it through apphttp.RedactURL — but note RedactURL only redacts
// query params, not path segments, so the URL is additionally kept out of every error.
func (d *driver) searchURL() string {
	return d.BaseURL + searchPath + d.Cfg["api_key"]
}

// post issues the JSON POST to api/torrents/{api_key}. The api_key rides in the URL path
// and the rsskey rides inside the body, so neither the URL nor the body is ever logged.
// Content-Type and Accept are application/json (Prowlarr sets both). A transport error
// surfaces with the path-embedded api_key scrubbed (apphttp.RedactError + Base.ScrubErr,
// which preserves any wrapped sentinel — e.g. login.ErrLoginFailed — through the scrub);
// the raw URL is never placed in the error.
func (d *driver) post(ctx context.Context, body []byte) (*native.Response, error) {
	req, err := stdhttp.NewRequestWithContext(ctx, stdhttp.MethodPost, d.searchURL(), bytes.NewReader(body))
	if err != nil {
		// A build failure is a *url.Error that quotes the full URL — including the
		// path-embedded api_key — so route it through apphttp.RedactURLError, which
		// rebuilds it host-only (mirrors the grab build-request path in grab.go).
		return nil, fmt.Errorf("beyondhd: build request: %w", apphttp.RedactURLError(err))
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := d.Do(ctx, req, native.ClassifyAuth403)
	return resp, d.ScrubErr(err)
}
