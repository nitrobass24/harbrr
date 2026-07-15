package passthepopcorn

import (
	"context"
	"fmt"
	stdhttp "net/http"

	"github.com/autobrr/harbrr/internal/indexer/native"
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
	req.Header.Set(headerAPIUser, d.Cfg["apiuser"])
	req.Header.Set(headerAPIKey, d.Cfg["apikey"])
}

// get issues an authenticated GET to a PTP endpoint (search or download). The ApiUser/
// ApiKey credentials ride in headers — never in the URL and never logged — so the header
// is set but never recorded. accept sets the Accept header when non-empty: the search
// expects JSON, but a torrent download must not force JSON (a strict server could 406 or
// return a JSON error instead of the .torrent), so Grab passes an empty accept. A
// transport error surfaces only the endpoint's scheme://host through native.Base. The
// caller owns the returned body and interprets the status.
func (d *driver) get(ctx context.Context, rawurl, accept string, download bool) (*native.Response, error) {
	req, err := stdhttp.NewRequestWithContext(ctx, stdhttp.MethodGet, rawurl, nil)
	if err != nil {
		return nil, fmt.Errorf("passthepopcorn: build request: %w", err)
	}
	d.setAuth(req)
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	var resp *native.Response
	classify := native.ClassifyRateLimit403
	if download {
		resp, err = d.DoDownload(ctx, req, classify)
	} else {
		resp, err = d.Do(ctx, req, classify)
	}
	return resp, d.ScrubErr(err)
}
