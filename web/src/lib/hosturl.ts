// Default ports per app/client kind, used to prefill a create form's Port field the
// moment a Kind is picked. rtorrent has no universal default (its XMLRPC mount varies
// by setup) — its port field just starts empty. qui = 7476 (autobrr/qui's default),
// deluge = 58846 (its daemon RPC port, not an HTTP port).
export const DEFAULT_PORTS: Record<string, number | undefined> = {
  qbittorrent: 8080, sabnzbd: 8080, nzbget: 6789, qui: 7476, flood: 3000,
  "download-station": 5000, transmission: 9091, deluge: 58846,
  sonarr: 8989, radarr: 7878, lidarr: 8686, readarr: 8787, whisparr: 6969,
  "crossseed-v6": 2468,
}

// composeHostURL builds a canonical http(s) URL from split fields — trimmed, never a
// trailing slash, port omitted when blank. A reverse-proxy base path is supported by
// writing it straight into the Host field ("nginx/sonarr"): it attaches after the
// port, never before it. Never use URL.toString() here — it always appends a "/".
export function composeHostURL(scheme: "http" | "https", host: string, port: string): string {
  const trimmed = host.trim().replace(/\/+$/, "")
  if (trimmed === "") return ""
  const slash = trimmed.indexOf("/")
  const authority = slash === -1 ? trimmed : trimmed.slice(0, slash)
  const path = slash === -1 ? "" : trimmed.slice(slash + 1)
  return `${scheme}://${authority}${port ? `:${port}` : ""}${path ? `/${path}` : ""}`
}

// A scheme-default port (http:80, https:443) is normalized away by the WHATWG URL
// parser — parsed.port reads "" the same as no port at all. Recover an explicitly
// written one from the raw string so it round-trips instead of silently vanishing
// (which would rewrite the stored App identity on the next unrelated save).
const EXPLICIT_PORT = /^https?:\/\/[^/?#]*:(\d+)(?:[/?#]|$)/

// decomposeHostURL splits a stored App base URL back into the fields the edit form
// seeds from. Returns null for anything that isn't a parseable http(s) URL — notably
// Deluge's bare "host:port" daemon address, which the caller falls back to editing as
// a single raw field rather than guessing a scheme (that would corrupt the App identity
// on save; see AppsSection).
export function decomposeHostURL(url: string): { scheme: "http" | "https", host: string, port: string } | null {
  let parsed: URL
  try {
    parsed = new URL(url)
  } catch {
    return null
  }
  if (parsed.protocol !== "http:" && parsed.protocol !== "https:") return null
  const path = parsed.pathname === "/" ? "" : parsed.pathname.replace(/\/+$/, "")
  const port = parsed.port || (EXPLICIT_PORT.exec(url)?.[1] ?? "")
  return { scheme: parsed.protocol.slice(0, -1) as "http" | "https", host: `${parsed.hostname}${path}`, port }
}

// composeHostPort builds Deluge's bare daemon-RPC address ("host:port", or just "host"
// with no port typed) — never a URL.
export function composeHostPort(host: string, port: string): string {
  const trimmed = host.trim()
  return port ? `${trimmed}:${port}` : trimmed
}

// decomposeHostPort splits a stored "host:port" address on the LAST colon (so an IPv4
// host works unremarkably); a non-numeric or absent tail means the whole value is host.
export function decomposeHostPort(value: string): { host: string, port: string } {
  const trimmed = value.trim()
  const idx = trimmed.lastIndexOf(":")
  if (idx === -1) return { host: trimmed, port: "" }
  const tail = trimmed.slice(idx + 1)
  if (tail !== "" && /^\d+$/.test(tail)) return { host: trimmed.slice(0, idx), port: tail }
  return { host: trimmed, port: "" }
}

// parsePastedURL fans a pasted full URL out into scheme/host/port — used by
// HostPortFields' Host onChange so pasting "http://host:port/path" doesn't get
// double-composed into the Host field verbatim. Only matches when the value actually
// starts with a scheme; a bare "myhost" or a truncated "http:/" is not a paste to fan out.
export function parsePastedURL(value: string): { scheme: "http" | "https", host: string, port: string } | null {
  if (!value.startsWith("http://") && !value.startsWith("https://")) return null
  return decomposeHostURL(value)
}
