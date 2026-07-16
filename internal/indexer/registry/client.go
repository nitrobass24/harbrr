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
	"golang.org/x/net/publicsuffix"

	apphttp "github.com/autobrr/harbrr/internal/http"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/loader"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
)

// defaultHTTPTimeout bounds a tracker request when no timeout is configured.
const defaultHTTPTimeout = 60 * time.Second

// responseHeaderTimeout bounds how long the transport waits for a response's
// status line + headers after fully writing the request, so a tracker that
// accepts a connection but never responds fails fast instead of hanging until
// defaultHTTPTimeout (or a configured, possibly much longer, timeout).
const responseHeaderTimeout = 30 * time.Second

// newDoer builds the production HTTP client the engine drives for one instance:
// a per-instance cookie jar (so a login response's Set-Cookie carries into the
// search request) and a per-instance timeout, wrapped in a paced client that
// enforces per-host rate limits + bounded 429/503 backoff. Each engine gets its
// own jar, so instances never share session cookies; the paced client exposes it
// via CookieJar() (search.JarOwner) so the engine seeds login cookies into this
// SAME jar — the single cookie authority on the wire. It is the production
// doerFactory — tests inject a replay Doer instead.
//
// Secret redaction is enforced at the logging chokepoints — the engine redacts
// resolved URLs in its error text, the Torznab handler redacts before logging, and
// the server's request logger redacts query params — so the transport itself does
// no logging and needs no wrapper.
func newDoer(p ClientParams) (search.Doer, error) {
	// The publicsuffix list gives correct cross-subdomain cookie scoping (a def's
	// login can redirect between subdomains), matching login.New's fallback jar.
	jar, err := cookiejar.New(&cookiejar.Options{PublicSuffixList: publicsuffix.List})
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
	// RedirectPolicy keeps the stdlib follow behavior (incl. the 10-hop cap) for
	// login/download/native requests, while search-path requests — stamped with
	// apphttp.WithNoRedirectFollow — get their 3xx back raw so the engine can
	// honor `followredirect` manually and detect logged-out redirects (Jackett's
	// WebClient never auto-follows).
	base := &http.Client{Jar: jar, Timeout: timeout, CheckRedirect: apphttp.RedirectPolicy, Transport: transport}
	return newPacedDoer(base, p.RateInterval, p.Logger), nil
}

// buildTransport returns the per-instance HTTP transport: always a clone of the
// stdlib default transport, with ResponseHeaderTimeout applied, routed through
// the configured proxy (proxy_type + proxy_url) when set. HTTP/HTTPS proxies use
// Transport.Proxy; SOCKS5 uses an x/net/proxy ContextDialer via DialContext
// (net/http's env-proxy ignores SOCKS, so the dialer is explicit). A bad config
// fails loud. Error messages never include proxy_url (it may embed credentials).
// SOCKS4 is not yet supported (x/net/proxy has no socks4 dialer) — it fails loud.
func buildTransport(cfg map[string]string) (*http.Transport, error) {
	def, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return nil, errors.New("registry: default transport is not *http.Transport")
	}
	transport := def.Clone()
	transport.ResponseHeaderTimeout = responseHeaderTimeout

	proxyType := strings.ToLower(strings.TrimSpace(cfg["proxy_type"]))
	rawURL := strings.TrimSpace(cfg["proxy_url"])
	if proxyType == "" && rawURL == "" {
		return transport, nil
	}
	if rawURL == "" {
		return nil, fmt.Errorf("registry: proxy_type %q set but proxy_url is empty", proxyType)
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, errors.New("registry: proxy_url is not a valid URL")
	}

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
		return nil, fmt.Errorf("registry: unknown proxy_type %q (want http, https, socks5, socks5h)", proxyType)
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
