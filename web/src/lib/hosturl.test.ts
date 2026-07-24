import { describe, expect, it } from "vitest"
import { composeHostPort, composeHostURL, decomposeHostPort, decomposeHostURL, parsePastedURL } from "./hosturl"

describe("composeHostURL", () => {
  it.each([
    // [name, scheme, host, port, expected]
    ["scheme + host + port", "http", "example.com", "8989", "http://example.com:8989"],
    ["empty port omitted", "https", "example.com", "", "https://example.com"],
    ["whitespace trimmed", "http", "  example.com  ", "8989", "http://example.com:8989"],
    ["trailing slash stripped", "http", "example.com/", "8989", "http://example.com:8989"],
    ["a reverse-proxy base path attaches after the port", "http", "nginx/sonarr", "8989", "http://nginx:8989/sonarr"],
    ["a base path with no port", "http", "nginx/sonarr/v3", "", "http://nginx/sonarr/v3"],
    ["empty host -> empty string", "http", "", "8989", ""],
    ["whitespace-only host -> empty string", "http", "   ", "8989", ""],
  ] as [string, "http" | "https", string, string, string][])("%s", (_name, scheme, host, port, expected) => {
    expect(composeHostURL(scheme, host, port)).toBe(expected)
  })

  it("never produces a trailing slash", () => {
    expect(composeHostURL("http", "example.com", "8989")).not.toMatch(/\/$/)
    expect(composeHostURL("http", "nginx/sonarr", "")).not.toMatch(/\/$/)
  })
})

describe("decomposeHostURL", () => {
  it.each([
    ["http://x:8080", { scheme: "http", host: "x", port: "8080" }],
    ["https://x", { scheme: "https", host: "x", port: "" }],
    ["http://x:8080/base", { scheme: "http", host: "x/base", port: "8080" }],
    // A scheme-default port is normalized away by URL.port ("") — recovered from the
    // raw string so a save that never touches Host/Port doesn't silently drop it and
    // rewrite the stored App identity.
    ["http://x:80", { scheme: "http", host: "x", port: "80" }],
    ["https://x:443", { scheme: "https", host: "x", port: "443" }],
    ["http://x:80/base", { scheme: "http", host: "x/base", port: "80" }],
  ])("%s", (url, expected) => {
    expect(decomposeHostURL(url)).toEqual(expected)
  })

  it("returns null for a non-http(s) or unparseable value", () => {
    expect(decomposeHostURL("localhost:58846")).toBeNull()
    expect(decomposeHostURL("ftp://x")).toBeNull()
    expect(decomposeHostURL("not a url")).toBeNull()
  })

  // The dedup-safety property: App identity is exact-string-equality on (kind, base_url),
  // so a trailing-slash input must recompose to the same canonical, slash-free string as
  // one typed without it — otherwise the same address could mint a duplicate App.
  it.each([
    "http://x:8080",
    "https://x",
    "http://x:8080/base",
    "http://x:8080/",
    "http://x:80",
    "https://x:443",
  ])("%s round-trips through composeHostURL to a canonical, slash-free form", (url) => {
    const parts = decomposeHostURL(url)
    expect(parts).not.toBeNull()
    expect(composeHostURL(parts!.scheme, parts!.host, parts!.port)).toBe(url.replace(/\/$/, ""))
  })
})

describe("composeHostPort / decomposeHostPort (Deluge)", () => {
  it.each([
    ["localhost", "58846", "localhost:58846"],
    ["localhost", "", "localhost"],
    ["192.168.1.1", "58846", "192.168.1.1:58846"],
    ["  localhost  ", "58846", "localhost:58846"],
  ])("compose(%s, %s) -> %s", (host, port, expected) => {
    expect(composeHostPort(host, port)).toBe(expected)
  })

  it.each([
    ["localhost:58846", { host: "localhost", port: "58846" }],
    ["localhost", { host: "localhost", port: "" }],
    ["192.168.1.1:58846", { host: "192.168.1.1", port: "58846" }],
    ["  localhost:58846  ", { host: "localhost", port: "58846" }],
  ])("decompose(%s) -> %o", (value, expected) => {
    expect(decomposeHostPort(value)).toEqual(expected)
  })

  it.each([
    ["localhost", "58846"],
    ["192.168.1.1", "58846"],
    ["localhost", ""],
  ])("round-trips host=%s port=%s", (host, port) => {
    expect(decomposeHostPort(composeHostPort(host, port))).toEqual({ host, port })
  })
})

describe("parsePastedURL", () => {
  it("fans a full URL out into its parts", () => {
    expect(parsePastedURL("http://sonarr:8989")).toEqual({ scheme: "http", host: "sonarr", port: "8989" })
    expect(parsePastedURL("https://sonarr")).toEqual({ scheme: "https", host: "sonarr", port: "" })
  })

  it.each(["http:/", "myhost", "myhost:8989", ""])("returns null for %s (not a full http(s) URL)", (value) => {
    expect(parsePastedURL(value)).toBeNull()
  })
})
