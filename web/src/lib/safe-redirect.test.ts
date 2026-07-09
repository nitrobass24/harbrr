import { describe, expect, it } from "vitest"
import { safeRedirectPath } from "./safe-redirect"

describe("safeRedirectPath", () => {
  it.each([
    // [name, input, expected]
    ["a plain in-app path", "/indexers", "/indexers"],
    ["a path with query string", "/a/b?x=1", "/a/b?x=1"],
    ["a path with a colon in a segment", "/a:b/c", "/a:b/c"],
    ["undefined", undefined, "/"],
    ["empty string", "", "/"],
    ["protocol-relative //host", "//evil.com", "/"],
    ["protocol-relative /\\host", "/\\evil.com", "/"],
    ["an absolute https URL", "https://evil.com", "/"],
    ["a javascript: scheme", "javascript:alert(1)", "/"],
    ["a bare relative token", "not-a-path", "/"],
    ["a backslash-bearing path", "/a\\b", "/"],
    ["a newline-smuggled path", "/a\nb", "/"],
    ["a tab-smuggled path", "/a\tb", "/"],
    ["a DEL-smuggled path", "/a\x7fb", "/"],
    // The guard rejects rather than trims, so a leading-whitespace scheme fails
    // startsWith("/") — pinning that we must never .trim() the value first.
    ["a leading-whitespace scheme", "  https://evil.com", "/"],
    // A fullwidth solidus (U+FF0F) is a different codepoint, not "/".
    ["a fullwidth-solidus host", "／evil.com", "/"],
  ])("maps %s → %s", (_name, input, expected) => {
    expect(safeRedirectPath(input)).toBe(expected)
  })
})
