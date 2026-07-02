package gazelle

import (
	"context"
	"fmt"
	stdhttp "net/http"

	apphttp "github.com/autobrr/harbrr/internal/http"
)

// authHeader builds the Authorization header value for the configured site: the
// per-site prefix ("" for RED, "token " for OPS) concatenated with the API key. The
// returned string is secret-bearing and MUST NEVER be logged.
func (d *driver) authHeader() string {
	return d.profile.authPrefix + d.cfg["apikey"]
}

// get issues an authenticated GET to a Gazelle endpoint (browse or download). The API
// key rides in the Authorization header — never in the URL and never logged — so the
// header is set but never recorded; Accept advertises JSON. A transport error routes
// the URL (which carries no secret) through apphttp.RedactURL. The caller owns the
// returned body and interprets the status.
func (d *driver) get(ctx context.Context, rawurl string) (*stdhttp.Response, error) {
	req, err := stdhttp.NewRequestWithContext(ctx, stdhttp.MethodGet, rawurl, nil)
	if err != nil {
		return nil, fmt.Errorf("gazelle: build request: %w", err)
	}
	req.Header.Set("Authorization", d.authHeader())
	req.Header.Set("Accept", "application/json")
	resp, err := d.doer.Do(req)
	if err != nil {
		return nil, fmt.Errorf("gazelle: request to %s: %w", apphttp.RedactURL(rawurl), err)
	}
	return resp, nil
}
