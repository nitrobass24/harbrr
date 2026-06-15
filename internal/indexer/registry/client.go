package registry

import (
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"time"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/loader"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
)

// defaultHTTPTimeout bounds a tracker request when no timeout is configured.
const defaultHTTPTimeout = 60 * time.Second

// newDoer builds the production HTTP client the engine drives for one instance:
// a per-instance cookie jar (so a login response's Set-Cookie carries into the
// search request) and a per-instance timeout, wrapped in a paced client that
// enforces per-host rate limits + bounded 429/503 backoff. Each engine gets its
// own jar, so instances never share session cookies. It is the production
// doerFactory — tests inject a replay Doer instead.
//
// Secret redaction is enforced at the logging chokepoints — the engine redacts
// resolved URLs in its error text, the Torznab handler redacts before logging, and
// the server's request logger redacts query params — so the transport itself does
// no logging and needs no wrapper.
func newDoer(p ClientParams) (search.Doer, error) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, fmt.Errorf("registry: new cookie jar: %w", err)
	}
	timeout := p.Timeout
	if timeout <= 0 {
		timeout = defaultHTTPTimeout
	}
	base := &http.Client{Jar: jar, Timeout: timeout}
	return newPacedDoer(base, p.RateInterval), nil
}

// resolveTimeout picks the per-instance request timeout: a "timeout" setting
// (Go duration, e.g. "30s") when present and valid, else the registry default.
func resolveTimeout(cfg map[string]string, fallback time.Duration) time.Duration {
	if v := cfg["timeout"]; v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			return d
		}
	}
	return fallback
}

// rateInterval picks the per-host spacing: the definition's requestDelay (seconds)
// when set, else defaultRateInterval.
func rateInterval(def *loader.Definition) time.Duration {
	if def != nil && def.RequestDelay != nil && *def.RequestDelay > 0 {
		return time.Duration(*def.RequestDelay * float64(time.Second))
	}
	return defaultRateInterval
}
