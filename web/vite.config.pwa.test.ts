import { describe, expect, it } from "vitest"

import { pwaOptions } from "./vite.config"

describe("pwa manifest + workbox config", () => {
  it("names the app and installs standalone from /", () => {
    expect(pwaOptions.manifest.name).toBe("harbrr")
    expect(pwaOptions.manifest.short_name).toBe("harbrr")
    expect(pwaOptions.manifest.display).toBe("standalone")
    expect(pwaOptions.manifest.start_url).toBe("/")
  })

  it("ships the 192 and 512 icons", () => {
    const sizes = pwaOptions.manifest.icons.map((icon) => icon.sizes)
    expect(sizes).toEqual(["192x192", "512x512"])
  })

  it("never lets /api hit the precache or the SPA navigation fallback", () => {
    for (const pattern of pwaOptions.workbox.globPatterns) {
      expect(pattern).not.toMatch(/api/)
    }
    expect(pwaOptions.workbox.navigateFallbackDenylist.some((re) => re.test("/api/indexers"))).toBe(true)
  })

  it("routes /api requests to NetworkOnly (matching the full URL, as Workbox does)", () => {
    const matches = (href: string) =>
      pwaOptions.workbox.runtimeCaching.find((route) => route.urlPattern({ url: new URL(href) }))
    expect(matches("https://harbrr.local/api/indexers")?.handler).toBe("NetworkOnly")
    expect(matches("https://harbrr.local/assets/index.js")).toBeUndefined()
  })
})
