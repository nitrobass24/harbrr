package filelist

import (
	"context"
	"encoding/base64"
	"fmt"
	stdhttp "net/http"
	"strings"

	apphttp "github.com/autobrr/harbrr/internal/http"
	"github.com/autobrr/harbrr/internal/indexer/native"
)

// basicAuthHeader builds the Authorization: Basic value from the configured username
// and passkey (both trimmed, matching Prowlarr's BasicNetworkCredential). The passkey
// is a secret: it lives only in this header, never the URL or a log. The base64 form
// is not a redaction (it is trivially reversible) — the protection is that this value
// is never logged and the raw passkey never enters a recorded URL.
func (d *driver) basicAuthHeader() string {
	user := strings.TrimSpace(d.Cfg["username"])
	pass := strings.TrimSpace(d.Cfg["passkey"])
	raw := user + ":" + pass
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(raw))
}

// get issues an authenticated GET with the Basic header. accept sets the Accept
// header when non-empty: the search expects JSON, but a torrent download must not
// force JSON (a strict server could 406 or return JSON instead of the .torrent). The
// caller owns the returned body and interprets the status. The URL may carry the
// passkey in its query on a download (Grab builds it), so a transport error surfaces
// only its scheme://host (apphttp.SchemeHost drops the query); the raw passkey never
// appears in a request the *search* issues (the passkey rides as a header there).
func (d *driver) get(ctx context.Context, rawurl, accept string, download bool) (*native.Response, error) {
	req, err := stdhttp.NewRequestWithContext(ctx, stdhttp.MethodGet, rawurl, nil)
	if err != nil {
		return nil, fmt.Errorf("filelist: build request: %w", apphttp.RedactURLError(err))
	}
	req.Header.Set("Authorization", d.basicAuthHeader())
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	if download {
		return d.DoDownload(ctx, req, native.ClassifyAuth403)
	}
	return d.Do(ctx, req, native.ClassifyAuth403)
}

// scrubPasskey removes any occurrence of the configured passkey from s. A hostile or
// buggy server could echo a submitted passkey (e.g. in an error body), and RedactURL
// only catches a key=value query shape — not free prose — so the raw passkey value is
// scrubbed before it can reach an error string or a persisted health-event detail.
func scrubPasskey(s string, cfg map[string]string) string {
	if pass := strings.TrimSpace(cfg["passkey"]); pass != "" {
		s = strings.ReplaceAll(s, pass, "[redacted]")
	}
	return s
}
