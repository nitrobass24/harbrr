package registry

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"time"

	"golang.org/x/net/proxy"

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
	transport, err := buildTransport(p.Cfg)
	if err != nil {
		return nil, err
	}
	base := &http.Client{Jar: jar, Timeout: timeout, Transport: transport}
	return newPacedDoer(base, p.RateInterval), nil
}

// buildTransport returns the per-instance HTTP transport: a clone of the default
// transport routed through the configured proxy (proxy_type + proxy_url), or nil
// (the stdlib default transport) when no proxy is set. HTTP/HTTPS proxies use
// Transport.Proxy; SOCKS5 uses an x/net/proxy ContextDialer via DialContext
// (net/http's env-proxy ignores SOCKS, so the dialer is explicit). A bad config
// fails loud. Error messages never include proxy_url (it may embed credentials).
// SOCKS4 is not yet supported (x/net/proxy has no socks4 dialer) — it fails loud.
func buildTransport(cfg map[string]string) (*http.Transport, error) {
	proxyType := strings.ToLower(strings.TrimSpace(cfg["proxy_type"]))
	rawURL := strings.TrimSpace(cfg["proxy_url"])
	if proxyType == "" && rawURL == "" {
		return nil, nil //nolint:nilnil // nil transport => the stdlib default (no proxy)
	}
	if rawURL == "" {
		return nil, fmt.Errorf("registry: proxy_type %q set but proxy_url is empty", proxyType)
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, errors.New("registry: proxy_url is not a valid URL")
	}

	def, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return nil, errors.New("registry: default transport is not *http.Transport")
	}
	transport := def.Clone()

	switch proxyType {
	case "http", "https":
		transport.Proxy = http.ProxyURL(u)
	case "socks5", "socks5h":
		dialer, derr := proxy.FromURL(u, proxy.Direct)
		if derr != nil {
			return nil, errors.New("registry: invalid socks5 proxy_url")
		}
		cd, ok := dialer.(proxy.ContextDialer)
		if !ok {
			return nil, errors.New("registry: socks5 proxy dialer has no DialContext")
		}
		transport.DialContext = cd.DialContext
		// The cloned default transport carries Proxy=ProxyFromEnvironment; clear it
		// so a stray HTTP(S)_PROXY env var can't layer an HTTP proxy over SOCKS5.
		transport.Proxy = nil
	case "socks4", "socks4a":
		return nil, fmt.Errorf("registry: proxy_type %q is not supported (use socks5 or http)", proxyType)
	default:
		return nil, fmt.Errorf("registry: unknown proxy_type %q (want http, https, socks5)", proxyType)
	}
	return transport, nil
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
