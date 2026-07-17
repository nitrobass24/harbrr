package gazelle

import (
	"context"
	"fmt"
	stdhttp "net/http"
)

// newRequest builds a GET request and hands it to the site's auth strategy to attach
// credentials/session (Authorization header for apiKeyAuth, session cookie/User-Agent
// for formLoginAuth — see strategy.go/strategy_formlogin.go). Transport, status
// classification, and redaction all live in the base Do/DoDownload the request is
// handed to afterward.
func (d *driver) newRequest(ctx context.Context, rawURL string) (*stdhttp.Request, error) {
	req, err := stdhttp.NewRequestWithContext(ctx, stdhttp.MethodGet, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("gazelle: build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	if err := d.site.strategy.Prepare(ctx, d, req); err != nil {
		return nil, err
	}
	return req, nil
}
