package gazellegames

import (
	"context"
	"fmt"
	stdhttp "net/http"
	"strings"

	apphttp "github.com/autobrr/harbrr/internal/http"
)

// apiKeyHeader is the GGn auth header. The api.php endpoint authenticates every request by
// the X-API-Key header (confirmed in autobrr ggn.go and Prowlarr GazelleGames); the value
// is the secret and MUST NEVER be logged.
const apiKeyHeader = "X-API-Key" //nolint:gosec // header NAME, not a credential value

// get issues an authenticated GET to a GGn endpoint (api.php search or a torrents.php
// download). The API key rides in the X-API-Key header — never in the URL and never logged
// — so the header is set but never recorded; Accept advertises JSON. A transport error
// routes the URL (which, for the api.php search, carries no secret) through
// apphttp.RedactURL so a passkey-bearing download URL can never leak. The caller owns the
// returned body and interprets the status.
func (d *driver) get(ctx context.Context, rawurl string) (*stdhttp.Response, error) {
	req, err := stdhttp.NewRequestWithContext(ctx, stdhttp.MethodGet, rawurl, nil)
	if err != nil {
		return nil, fmt.Errorf("gazellegames: build request: %w", err)
	}
	req.Header.Set(apiKeyHeader, strings.TrimSpace(d.cfg["apikey"]))
	req.Header.Set("Accept", "application/json")
	resp, err := d.doer.Do(req)
	if err != nil {
		return nil, fmt.Errorf("gazellegames: request to %s: %w", apphttp.RedactURL(rawurl), err)
	}
	return resp, nil
}

// scrubSecrets removes the configured apikey (and any persisted passkey) from s so a
// transport/server message echo cannot leak a secret. It mirrors scrubAPIKey but also
// covers the download passkey, which the X-API-Key header never carries but a rebuilt URL
// could surface.
func (d *driver) scrubSecrets(s string) string {
	s = d.scrubAPIKey(s)
	if pass := strings.TrimSpace(d.cfg["passkey"]); pass != "" {
		s = strings.ReplaceAll(s, pass, "[redacted]")
	}
	return s
}
