package animebytes

import (
	"context"
	"errors"
	"fmt"
	stdhttp "net/http"

	apphttp "github.com/autobrr/harbrr/internal/http"
	"github.com/autobrr/harbrr/internal/indexer/native"
)

// get issues an authenticated GET against an AnimeBytes URL. AnimeBytes carries both the
// username and the passkey (torrent_pass) in the request, so the URL itself is
// secret-bearing: it is NEVER logged, and both a build-request and a transport error
// surface only its scheme://host (apphttp.SchemeHost / apphttp.RedactURLError, which drop
// the path and query where the passkey lives) before the URL reaches the wrapped error.
// accept sets the Accept header — "application/json" for a scrape.php query, empty for a
// .torrent download so JSON is not forced on binary bytes.
func (d *driver) get(ctx context.Context, rawurl, accept string, download bool) (*native.Response, error) {
	if d.Doer == nil {
		return nil, errors.New("animebytes: nil request doer")
	}
	req, err := stdhttp.NewRequestWithContext(ctx, stdhttp.MethodGet, rawurl, nil)
	if err != nil {
		// The build error is a *url.Error quoting the full (passkey-bearing) URL, so it
		// is routed through apphttp.RedactURLError, which rebuilds it host-only.
		return nil, fmt.Errorf("animebytes: build request: %w", apphttp.RedactURLError(err))
	}
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	if download {
		return d.DoDownload(ctx, req, native.ClassifyAuth403)
	}
	return d.Do(ctx, req, native.ClassifyAuth403)
}
