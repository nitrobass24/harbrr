import { describe, expect, it } from "vitest"
import { isSafeHref } from "./safe-href"

describe("isSafeHref", () => {
  const HTTP = ["http:", "https:"]
  const MAGNET = ["magnet:"]

  it("allows http and https for the download link", () => {
    expect(isSafeHref("http://tracker.example/dl?id=1&passkey=NOTREAL", HTTP)).toBe(true)
    expect(isSafeHref("https://tracker.example/dl?id=1&passkey=NOTREAL", HTTP)).toBe(true)
  })

  it("allows magnet: for the magnet link", () => {
    expect(isSafeHref("magnet:?xt=urn:btih:abc", MAGNET)).toBe(true)
  })

  it("rejects magnet against the http allowlist and vice versa", () => {
    expect(isSafeHref("magnet:?xt=urn:btih:abc", HTTP)).toBe(false)
    expect(isSafeHref("http://tracker.example/dl", MAGNET)).toBe(false)
  })

  it("rejects javascript:", () => {
    expect(isSafeHref("javascript:fetch('/api/keys',{method:'DELETE'})", HTTP)).toBe(false)
    expect(isSafeHref("javascript:alert(1)", MAGNET)).toBe(false)
  })

  it("rejects other dangerous schemes", () => {
    expect(isSafeHref("data:text/html,<script>alert(1)</script>", HTTP)).toBe(false)
    expect(isSafeHref("vbscript:msgbox(1)", HTTP)).toBe(false)
    expect(isSafeHref("blob:http://tracker.example/uuid", HTTP)).toBe(false)
    expect(isSafeHref("file:///etc/passwd", HTTP)).toBe(false)
  })

  it("rejects a scheme obfuscated with an embedded tab", () => {
    expect(isSafeHref("java\tscript:alert(1)", HTTP)).toBe(false)
  })

  it("rejects a scheme obfuscated with leading whitespace", () => {
    expect(isSafeHref(" javascript:alert(1)", HTTP)).toBe(false)
    expect(isSafeHref("\njavascript:alert(1)", HTTP)).toBe(false)
  })

  it("is case-insensitive for the scheme", () => {
    expect(isSafeHref("JavaScript:alert(1)", HTTP)).toBe(false)
    expect(isSafeHref("HTTP://tracker.example/dl", HTTP)).toBe(true)
    expect(isSafeHref("MAGNET:?xt=urn:btih:abc", MAGNET)).toBe(true)
  })

  it("rejects relative, blank, and unparseable values", () => {
    expect(isSafeHref("", HTTP)).toBe(false)
    expect(isSafeHref(undefined, HTTP)).toBe(false)
    expect(isSafeHref(null, HTTP)).toBe(false)
    expect(isSafeHref("/api/download?id=1", HTTP)).toBe(false)
  })

  it("passes the original string through unmodified when valid (no rebuilding)", () => {
    // isSafeHref only returns a boolean; callers must render the original string.
    const original = "http://tracker.example/dl?id=1&passkey=NOTREAL"
    expect(isSafeHref(original, HTTP)).toBe(true)
    expect(original).toBe("http://tracker.example/dl?id=1&passkey=NOTREAL")
  })
})
