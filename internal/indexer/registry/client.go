package registry

import (
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"time"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
)

// defaultHTTPTimeout bounds a tracker request when no timeout is configured.
const defaultHTTPTimeout = 60 * time.Second

// newDoer builds the production HTTP client the engine drives for one instance:
// a per-instance cookie jar (so a login response's Set-Cookie carries into the
// search request) and a timeout. Each engine gets its own jar, so instances never
// share session cookies.
//
// Secret redaction is enforced at the logging chokepoints — the engine redacts
// resolved URLs in its error text, the Torznab handler redacts before logging, and
// the server's request logger redacts query params — so the transport itself does
// no logging and needs no wrapper.
func newDoer(timeout time.Duration) (search.Doer, error) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, fmt.Errorf("registry: new cookie jar: %w", err)
	}
	if timeout <= 0 {
		timeout = defaultHTTPTimeout
	}
	return &http.Client{Jar: jar, Timeout: timeout}, nil
}
