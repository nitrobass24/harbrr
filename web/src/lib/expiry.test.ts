import { describe, expect, it } from "vitest"
import { daysUntil, expirySortKey, expiryState } from "@/lib/expiry"
import type { Instance } from "@/lib/api"

// NOW is fixed so "in 7 days" means the same thing on every machine and in every month.
const NOW = new Date("2026-07-25T09:00:00Z")

function instance(over: Partial<Instance> = {}): Instance {
  return {
    id: 1, slug: "tt", definitionId: "tt", name: "TT", enabled: true, protocol: "torrent",
    freeleech: false, priority: 25, minSeeders: 0, syncCategories: [],
    enableRss: true, enableAutomaticSearch: true, enableInteractiveSearch: true,
    expiresAt: "", expiryKind: "", expiryLifetime: false,
    createdAt: "2026-01-01T00:00:00Z", updatedAt: "2026-01-01T00:00:00Z",
    ...over,
  }
}

describe("daysUntil", () => {
  it.each([
    ["a week ahead", "2026-08-01", 7],
    ["today", "2026-07-25", 0],
    ["already past", "2026-07-20", -5],
  ])("%s", (_name, date, want) => {
    expect(daysUntil(date, NOW)).toBe(want)
  })

  it("returns null for a value that is not a date", () => {
    expect(daysUntil("whenever", NOW)).toBeNull()
  })
})

describe("expiryState", () => {
  it("says nothing at all when no expiry is tracked", () => {
    const s = expiryState(instance(), NOW)
    expect(s.label).toBe("—")
    expect(s.days).toBeNull()
    expect(s.tone).toBe("text-faint")
  })

  it("reads Lifetime and ignores any date behind it", () => {
    const s = expiryState(instance({ expiryLifetime: true, expiresAt: "2026-07-26" }), NOW)
    expect(s.label).toBe("Lifetime")
    expect(s.days).toBeNull()
  })

  it("shows a distant expiry as a plain date, with no alarm", () => {
    const s = expiryState(instance({ expiresAt: "2027-01-01" }), NOW)
    expect(s.label).toBe("2027-01-01")
    expect(s.tone).toBe("text-muted-foreground")
  })

  it("turns into a countdown inside the last week", () => {
    const s = expiryState(instance({ expiresAt: "2026-07-28" }), NOW)
    expect(s.label).toBe("3d")
    expect(s.tone).toBe("text-warn")
  })

  it("keeps saying Expired, persistently, after the date has passed", () => {
    const s = expiryState(instance({ expiresAt: "2026-01-01", expiryKind: "account" }), NOW)
    expect(s.label).toBe("Expired")
    expect(s.tone).toBe("text-bad")
    expect(s.detail).toContain("Account")
    // Far past, not just a day past — the row must not quietly go neutral again.
    expect(expiryState(instance({ expiresAt: "2020-01-01" }), NOW).label).toBe("Expired")
  })
})

describe("expirySortKey", () => {
  it("orders expired first, then soonest, with untracked and lifetime last", () => {
    const rows = [
      instance({ slug: "untracked" }),
      instance({ slug: "far", expiresAt: "2027-01-01" }),
      instance({ slug: "expired", expiresAt: "2026-06-01" }),
      instance({ slug: "lifetime", expiryLifetime: true }),
      instance({ slug: "soon", expiresAt: "2026-07-28" }),
    ]
    const order = [...rows]
      .sort((a, b) => expirySortKey(a, NOW) - expirySortKey(b, NOW))
      .map((r) => r.slug)
    expect(order).toEqual(["expired", "soon", "far", "untracked", "lifetime"])
  })
})
