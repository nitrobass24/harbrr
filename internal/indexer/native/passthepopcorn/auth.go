package passthepopcorn

import (
	"context"
	"fmt"
	stdhttp "net/http"
	"strings"

	apphttp "github.com/autobrr/harbrr/internal/http"
)

// PTP authenticates every request with two HTTP headers, exact casing "ApiUser" and
// "ApiKey" (Prowlarr PassThePopcornRequestGenerator / autobrr pkg/ptp). There is no
// cookie, login round-trip, or passkey in the URL: auth is stateless per request, so the
// same two headers re-attach to the search request and (in the grab leaf) the download.
const (
	headerAPIUser = "ApiUser"
	headerAPIKey  = "ApiKey"
)

// setAuth attaches the two credential headers to a request. BOTH values are secrets
// (Prowlarr PrivacyLevel.UserName / PrivacyLevel.ApiKey), so the headers MUST NEVER be
// logged. The credentials ride only in headers — never the URL — so the request URL stays
// secret-free and safe to record.
func (d *driver) setAuth(req *stdhttp.Request) {
	req.Header.Set(headerAPIUser, d.cfg["apiuser"])
	req.Header.Set(headerAPIKey, d.cfg["apikey"])
}

// get issues an authenticated GET to a PTP endpoint (search or download). The ApiUser/
// ApiKey credentials ride in headers — never in the URL and never logged — so the header
// is set but never recorded; Accept advertises JSON. A transport error routes the URL
// (which carries no secret) through apphttp.RedactURL. The caller owns the returned body
// and interprets the status.
func (d *driver) get(ctx context.Context, rawurl string) (*stdhttp.Response, error) {
	req, err := stdhttp.NewRequestWithContext(ctx, stdhttp.MethodGet, rawurl, nil)
	if err != nil {
		return nil, fmt.Errorf("passthepopcorn: build request: %w", err)
	}
	d.setAuth(req)
	req.Header.Set("Accept", "application/json")
	resp, err := d.doer.Do(req)
	if err != nil {
		return nil, fmt.Errorf("passthepopcorn: request to %s: %w", apphttp.RedactURL(rawurl), err)
	}
	return resp, nil
}

// scrubSecrets removes the configured ApiUser and ApiKey from s so a server echo (e.g. in
// an error message or response body) cannot leak either credential. Mirrors
// broadcastthenet.scrubAPIKey; both credentials ride only in headers and are never
// logged, but any error string is scrubbed defensively before it can surface.
func (d *driver) scrubSecrets(s string) string {
	for _, key := range []string{"apikey", "apiuser"} {
		if v := strings.TrimSpace(d.cfg[key]); v != "" {
			s = strings.ReplaceAll(s, v, "[redacted]")
		}
	}
	return s
}
