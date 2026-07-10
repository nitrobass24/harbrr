package torrentday

import (
	"context"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
)

// Test verifies the configured session cookie still authenticates (the management
// "test indexer" action) by issuing an empty browse query. A good cookie returns 200 with
// a JSON array; a stale cookie redirects to /login.php (or returns 401/403). Search stamps
// the context WithNoRedirectFollow, so that redirect surfaces as a raw 3xx that
// isLoginRedirect maps to login.ErrLoginFailed (the registry records an auth_failure health
// event) instead of being followed to the login page and misread as a parse error.
// Rate-limit and transport errors propagate unchanged (the cookie is scrubbed by Search's get).
func (d *driver) Test(ctx context.Context) error {
	_, err := d.Search(ctx, search.Query{})
	return err
}
