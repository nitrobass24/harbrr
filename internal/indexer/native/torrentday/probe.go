package torrentday

import (
	"context"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
)

// Test verifies the configured session cookie still authenticates (the management
// "test indexer" action) by issuing an empty browse query. A good cookie returns 200 with
// a JSON array; a stale cookie redirects to /login.php (or returns 401/403), which Search
// already maps to login.ErrLoginFailed so the registry records an auth_failure health
// event. Rate-limit and transport errors propagate unchanged (the cookie is scrubbed by
// Search's get).
func (d *driver) Test(ctx context.Context) error {
	_, err := d.Search(ctx, search.Query{})
	return err
}
