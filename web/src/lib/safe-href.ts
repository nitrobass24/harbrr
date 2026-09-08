// Tracker-controlled URLs (release.link / release.magnet) are rendered verbatim as
// anchor hrefs. A hostile or compromised tracker could return a "javascript:" (or
// "data:", "vbscript:", etc.) URL that executes in the authenticated management
// origin when clicked. This is a permit-known-good ALLOWLIST — new/unknown schemes
// are rejected by default rather than trying to enumerate every dangerous one.
//
// Only the scheme is validated here; the caller must render the ORIGINAL string
// unmodified (it may carry a passkey in the query string that must not be mangled).
export function isSafeHref(value: string | null | undefined, allowedSchemes: string[]): boolean {
  if (!value) return false

  try {
    // The WHATWG URL parser lowercases the scheme and handles opaque schemes
    // (e.g. "magnet:", "javascript:") as well as hierarchical ones (http/https).
    // It also does its own input cleanup first — embedded tab/newline/CR anywhere
    // in the URL and leading/trailing C0 controls and spaces are stripped before
    // the scheme is read — so "java\tscript:alert(1)" and " javascript:alert(1)"
    // both resolve to the "javascript" scheme a naive `startsWith` would miss.
    // `.protocol` includes the trailing colon (e.g. "http:"), matching the
    // allowedSchemes callers pass in ("http:", "https:", "magnet:").
    return allowedSchemes.includes(new URL(value).protocol)
  } catch {
    return false
  }
}
