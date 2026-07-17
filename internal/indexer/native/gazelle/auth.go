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
// newRequest also returns the session snapshot the strategy attached, so the caller can
// thread the request-used generation into the auth-failure path (renewal coalescing) and
// scrub the exact cookie this request sent — never a later re-snapshot a concurrent
// renewal could have advanced (apiKeyAuth has no session, so it returns the zero value).
func (d *driver) newRequest(ctx context.Context, rawURL string) (*stdhttp.Request, sessionState, error) {
	req, err := stdhttp.NewRequestWithContext(ctx, stdhttp.MethodGet, rawURL, nil)
	if err != nil {
		return nil, sessionState{}, fmt.Errorf("gazelle: build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	if err := d.site.strategy.Prepare(ctx, d, req); err != nil {
		return nil, sessionState{}, err
	}
	return req, d.sessionSnapshot(), nil
}
