// safeRedirectPath validates a post-login "return here" target, returning a
// same-origin in-app path or "/" whenever the value is absent or could steer the
// browser off-app. A logged-out visitor is bounced to /login?redirect=<path>, and
// an attacker who crafts /login?redirect=https://evil.com (or the protocol-relative
// //evil.com, or the "/\evil.com" form browsers also treat as protocol-relative)
// must NEVER be sent off-site on success. So only a single leading "/" followed by
// an ordinary path is accepted; anything else falls back to the dashboard.
export function safeRedirectPath(raw: string | undefined): string {
  if (!raw) return "/"
  // Must be a rooted, single-slash path. "//host" and "/\host" are protocol-
  // relative and would navigate to another origin, so reject both.
  if (!raw.startsWith("/") || raw.startsWith("//") || raw.startsWith("/\\")) return "/"
  // Backslashes (path/scheme smuggling) are never valid in an in-app path.
  if (raw.includes("\\")) return "/"
  // Control characters (incl. tab/newline/CR and DEL) can smuggle past naive
  // URL parsers; a legitimate in-app path never contains them.
  for (let i = 0; i < raw.length; i++) {
    const c = raw.charCodeAt(i)
    if (c < 0x20 || c === 0x7f) return "/"
  }
  return raw
}
