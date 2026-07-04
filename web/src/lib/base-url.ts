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

// defaultHarbrrUrl is the best-effort prefill for a form's "harbrr URL" field:
// how this browser reaches harbrr is usually how an app can too (the operator
// adjusts for container-network names as needed). Shared so the connection and
// announce forms cannot drift.
export function defaultHarbrrUrl(): string {
  return `${window.location.origin}${getBaseUrl()}`
}
