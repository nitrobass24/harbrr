import { afterEach, describe, expect, it } from "vitest"
import { getApiBaseUrl, getBaseUrl } from "./base-url"

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
