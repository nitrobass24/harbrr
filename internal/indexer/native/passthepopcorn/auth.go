package passthepopcorn

import (
	"context"
	"fmt"
	stdhttp "net/http"
	"sort"
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
// is set but never recorded. accept sets the Accept header when non-empty: the search
// expects JSON, but a torrent download must not force JSON (a strict server could 406 or
// return a JSON error instead of the .torrent), so Grab passes an empty accept. A
// transport error routes the URL (which carries no secret) through apphttp.RedactURL. The
// caller owns the returned body and interprets the status.
func (d *driver) get(ctx context.Context, rawurl, accept string) (*stdhttp.Response, error) {
	req, err := stdhttp.NewRequestWithContext(ctx, stdhttp.MethodGet, rawurl, nil)
	if err != nil {
		return nil, fmt.Errorf("passthepopcorn: build request: %w", err)
	}
	d.setAuth(req)
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
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
	secrets := make([]string, 0, 2)
	for _, key := range []string{"apikey", "apiuser"} {
		if v := strings.TrimSpace(d.cfg[key]); v != "" {
			secrets = append(secrets, v)
		}
	}
	// Redact the LONGER credential first: if one secret is a substring of the other
	// (e.g. ApiUser inside ApiKey), replacing the shorter first would mangle or
	// partially miss the longer one, leaking a fragment.
	sort.Slice(secrets, func(i, j int) bool { return len(secrets[i]) > len(secrets[j]) })
	for _, v := range secrets {
		s = strings.ReplaceAll(s, v, "[redacted]")
	}
	return s
}
