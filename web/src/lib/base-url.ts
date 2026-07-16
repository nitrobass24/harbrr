// The server injects window.__HARBRR_BASE_URL__ (and __HARBRR_VERSION__) into
// index.html at serve time; under `pnpm dev` neither is set and the base is "".

// getBaseUrl returns the base path with no trailing slash ("" at root), so the
// router basepath and API prefixes compose by simple concatenation.
export function getBaseUrl(): string {
  const raw = window.__HARBRR_BASE_URL__ ?? ""
  if (raw === "/") return ""
  return raw.endsWith("/") ? raw.slice(0, -1) : raw
}

// getApiBaseUrl returns the prefix every management-API call goes through.
export function getApiBaseUrl(): string {
  return `${getBaseUrl()}/api`
}

// defaultHarbrrUrl is the best-effort prefill for a form's "harbrr URL" field. When the
// operator configured server.external_url, it is authoritative (it's what the server
// itself uses for feed/announce links, cutting connection drift); otherwise how this
// browser reaches harbrr is usually how an app can too (the operator adjusts for
// container-network names as needed). Shared so the connection and announce forms
// cannot drift from each other.
export function defaultHarbrrUrl(): string {
  return window.__HARBRR_EXTERNAL_URL__ || `${window.location.origin}${getBaseUrl()}`
}

// explicitUrlPort returns the port a URL names outright, or null when it
// relies on the scheme's default or doesn't parse. A harbrrUrl fronted by a
// reverse proxy (docs/security.md's supported deployment) is typically
// written without one — TLS terminates on the proxy's standard 443/80 and
// that number has no relationship to harbrr's own internal listen port — so
// null means "not comparable to the listen port", never "assume 443/80".
// (The parser also normalizes away a port written as the scheme's own
// default, e.g. https:443, which reads the same as no port at all.)
export function explicitUrlPort(url: string): number | null {
  try {
    const port = new URL(url).port
    return port === "" ? null : Number(port)
  } catch {
    return null
  }
}

// withPort returns url with its port replaced, keeping scheme, host, and
// path — modulo WHATWG serialization: a path-less URL gains a trailing
// slash, hosts lowercase, and a scheme-default port is dropped entirely.
// Returns url unchanged when it doesn't parse.
export function withPort(url: string, port: number): string {
  try {
    const parsed = new URL(url)
    parsed.port = String(port)
    return parsed.toString()
  } catch {
    return url
  }
}
