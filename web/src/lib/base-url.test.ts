import { afterEach, describe, expect, it } from "vitest"
import { defaultHarbrrUrl, explicitUrlPort, getApiBaseUrl, getBaseUrl, withPort } from "./base-url"

describe("getBaseUrl", () => {
  afterEach(() => {
    delete window.__HARBRR_BASE_URL__
  })

  const cases: { name: string, injected: string | undefined, want: string }[] = [
    { name: "unset (dev server)", injected: undefined, want: "" },
    { name: "empty (root deploy)", injected: "", want: "" },
    { name: "bare slash normalizes to empty", injected: "/", want: "" },
    { name: "subpath", injected: "/harbrr", want: "/harbrr" },
    { name: "subpath with trailing slash", injected: "/harbrr/", want: "/harbrr" },
  ]

  for (const c of cases) {
    it(c.name, () => {
      if (c.injected !== undefined) window.__HARBRR_BASE_URL__ = c.injected
      expect(getBaseUrl()).toBe(c.want)
      expect(getApiBaseUrl()).toBe(`${c.want}/api`)
    })
  }
})

describe("defaultHarbrrUrl", () => {
  afterEach(() => {
    delete window.__HARBRR_EXTERNAL_URL__
    delete window.__HARBRR_BASE_URL__
  })

  it("prefers the configured external_url when set", () => {
    window.__HARBRR_EXTERNAL_URL__ = "https://harbrr.example.com"
    expect(defaultHarbrrUrl()).toBe("https://harbrr.example.com")
  })

  it("falls back to window.location.origin + base path when unset", () => {
    window.__HARBRR_BASE_URL__ = "/harbrr"
    expect(defaultHarbrrUrl()).toBe(`${window.location.origin}/harbrr`)
  })
})

describe("explicitUrlPort", () => {
  const cases: { name: string, url: string, want: number | null }[] = [
    { name: "explicit non-default port", url: "http://harbrr:7478", want: 7478 },
    // The URL parser normalizes away a port written explicitly as the
    // scheme's own default (https:443 here), so it reads the same as if
    // no port had been given at all.
    { name: "port written as the scheme default normalizes to no port", url: "https://harbrr:443", want: null },
    { name: "no port, https (typical reverse-proxy origin)", url: "https://harbrr.example.com", want: null },
    { name: "no port, http", url: "http://harbrr.example.com", want: null },
    { name: "path with no port", url: "https://harbrr.example.com/base", want: null },
    { name: "unparseable string", url: "not a url", want: null },
  ]

  for (const c of cases) {
    it(c.name, () => {
      expect(explicitUrlPort(c.url)).toBe(c.want)
    })
  }
})

describe("withPort", () => {
  const cases: { name: string, url: string, port: number, want: string }[] = [
    { name: "replaces an explicit port, path kept", url: "http://harbrr:7478/base", port: 9117, want: "http://harbrr:9117/base" },
    { name: "adds a port to a URL without one", url: "http://harbrr.example.com/base", port: 9117, want: "http://harbrr.example.com:9117/base" },
    // WHATWG serialization: a scheme-default port is dropped entirely, and a
    // path-less URL gains a trailing slash (see withPort's doc comment).
    { name: "scheme-default port drops, path-less gains a slash", url: "http://harbrr:8080", port: 80, want: "http://harbrr/" },
    { name: "https scheme-default port drops, path kept", url: "https://harbrr:8080/path", port: 443, want: "https://harbrr/path" },
    { name: "unparseable string returns unchanged", url: "not a url", port: 9117, want: "not a url" },
  ]

  for (const c of cases) {
    it(c.name, () => {
      expect(withPort(c.url, c.port)).toBe(c.want)
    })
  }
})
