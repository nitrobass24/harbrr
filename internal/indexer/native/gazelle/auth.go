package gazelle

import (
	"context"
	"fmt"
	stdhttp "net/http"
)

// authHeader builds the Authorization header value for the configured site: the
// per-site prefix ("" for RED, "token " for OPS) concatenated with the API key. The
// returned string is secret-bearing and MUST NEVER be logged.
func (d *driver) authHeader() string {
	return d.profile.authPrefix + d.Cfg["apikey"]
}

// newRequest builds an authenticated GET for a Gazelle endpoint (browse or download).
// The API key rides in the Authorization header — never in the URL and never logged —
// so the header is set but never recorded; Accept advertises JSON. Transport, status
// classification, and redaction all live in the base Do/DoDownload the request is
// handed to.
func (d *driver) newRequest(ctx context.Context, rawurl string) (*stdhttp.Request, error) {
	req, err := stdhttp.NewRequestWithContext(ctx, stdhttp.MethodGet, rawurl, nil)
	if err != nil {
		return nil, fmt.Errorf("gazelle: build request: %w", err)
	}
	req.Header.Set("Authorization", d.authHeader())
	req.Header.Set("Accept", "application/json")
	return req, nil
}
