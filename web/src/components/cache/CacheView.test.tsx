import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { fireEvent, render, screen } from "@testing-library/react"
import { describe, expect, it } from "vitest"

import { stubApi } from "@/test/stubApi"
import { CacheView } from "./CacheView"
import { breakerLabel, coverageNote, unixAgo } from "./cache-format"
import { safeInt } from "./safe-int"

// safeInt backs the numeric knob inputs (thinThreshold, refreshAheadPct). The
// bug it guards: Number("") is 0, not NaN, so clearing a field would commit 0 —
// silently disabling a knob (e.g. refreshAheadPct: 0 turns refresh-ahead off).
describe("safeInt", () => {
  it("keeps the previous value for an empty or whitespace-only field", () => {
    // The regression: without the trim guard these each return 0.
    expect(safeInt("", 25)).toBe(25)
    expect(safeInt("   ", 25)).toBe(25)
  })

  it("keeps the previous value for a partial/non-numeric entry", () => {
    expect(safeInt("-", 25)).toBe(25)
    expect(safeInt("abc", 25)).toBe(25)
  })

  it("parses a valid number, including an explicit zero", () => {
    expect(safeInt("0", 25)).toBe(0)
    expect(safeInt("7", 25)).toBe(7)
    expect(safeInt("100", 25)).toBe(100)
  })
})

// The age aggregates are nullable on the wire (null while the cache is empty).
// The bug guarded here: Number(null) * 1000 is a valid 1970 date, so an
// unguarded render says "20802d ago" instead of admitting it has nothing.
describe("unixAgo", () => {
  const now = Math.floor(Date.now() / 1000)

  it("renders an em dash for a missing timestamp", () => {
    expect(unixAgo(null)).toBe("—")
    expect(unixAgo(undefined)).toBe("—")
    expect(unixAgo(0)).toBe("—")
  })

  it("renders a real timestamp as a relative age", () => {
    expect(unixAgo(now - 120)).toBe("2m ago")
    expect(unixAgo(now - 7200)).toBe("2h ago")
  })
})

// breakerOpenUntil is a FUTURE instant, which relativeTime cannot express, and
// it can lapse before the row is reaped. Neither case may render a negative.
describe("breakerLabel", () => {
  const nowMs = 1_800_000_000_000
  const nowSec = nowMs / 1000

  it("counts down to the reopen instant", () => {
    expect(breakerLabel(nowSec + 30, nowMs)).toBe("breaker open · retries in 30s")
    expect(breakerLabel(nowSec + 240, nowMs)).toBe("breaker open · retries in 4m")
  })

  it("reads as retrying once the instant has lapsed", () => {
    // The regression: a bare subtraction renders "retries in -3m" here.
    const lapsed = breakerLabel(nowSec - 180, nowMs)
    expect(lapsed).toBe("breaker open · retrying")
    expect(lapsed).not.toContain("-")
  })

  it("treats the exact reopen instant as retrying, not 0s", () => {
    expect(breakerLabel(nowSec, nowMs)).toBe("breaker open · retrying")
  })
})

// The 1d/7d/30d views are IN-MEMORY hour buckets, so a freshly restarted process
// backs a "30d" tile with minutes of data. Without this caveat the UI would claim a
// month it does not have — the one way this feature can mislead.
describe("coverageNote", () => {
  const nowMs = 1_800_000_000_000
  const nowSec = nowMs / 1000

  it("warns when the window outruns how long the buckets have been collecting", () => {
    expect(coverageNote("30d", nowSec - 7200, nowMs)).toBe("in-memory window — only collecting since 2h ago")
    expect(coverageNote("7d", nowSec - 2 * 86_400, nowMs)).toContain("2d ago")
  })

  it("stays quiet once the window is genuinely covered", () => {
    expect(coverageNote("1d", nowSec - 2 * 86_400, nowMs)).toBeNull()
    expect(coverageNote("7d", nowSec - 8 * 86_400, nowMs)).toBeNull()
  })

  it("never caveats all-time — it reads the restart-persisted counters", () => {
    expect(coverageNote("all", nowSec - 60, nowMs)).toBeNull()
  })
})

const CONFIG = {
  enabled: true,
  rssTtl: "5m0s",
  keywordTtl: "30m0s",
  thinTtl: "2m0s",
  thinThreshold: 5,
  refreshAheadPct: 80,
  negativeTtl: "1m0s",
  cleanupInterval: "1h0m0s",
}

function renderCacheView(stats: unknown) {
  stubApi({ "GET /api/cache/config": CONFIG, "GET /api/cache/stats": stats })
  render(
    <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      <CacheView />
    </QueryClientProvider>
  )
}

describe("CacheView", () => {
  it("renders entry ages, a breaker countdown, and knob help", async () => {
    const now = Math.floor(Date.now() / 1000)
    renderCacheView({
      enabled: true,
      trackerHitsSaved: 128,
      hitRatio: 0.75,
      entries: 12,
      oldestCachedAt: now - 3600,
      newestCachedAt: now - 120,
      lastUsedAt: now - 30,
      byIndexer: [
        { instanceId: 1, name: "Open Tracker", breakerOpenUntil: now + 240 },
        { instanceId: 2, name: "Lapsed Tracker", breakerOpenUntil: now - 300 },
        { instanceId: 3, name: "Healthy Tracker", breakerOpenUntil: null },
      ],
    })

    expect(await screen.findByText(/Oldest entry 1h ago · newest 2m ago · last served just now/)).toBeTruthy()
    expect(screen.getByText("breaker open · retries in 4m")).toBeTruthy()
    expect(screen.getByText("breaker open · retrying")).toBeTruthy()
    expect(screen.getByText("breaker closed")).toBeTruthy()

    // Knob help: one plain-language line per knob, wired to its input.
    const help = await screen.findByText(/How long a real search/)
    expect(screen.getByLabelText("Keyword TTL").getAttribute("aria-describedby")).toBe(help.id)
    expect(screen.getByText(/How few results count as thin/)).toBeTruthy()
    expect(screen.getByText(/served instantly and refreshed in the background/)).toBeTruthy()
  })

  it("switches the tiles between windows with no refetch, and admits short coverage", async () => {
    const now = Math.floor(Date.now() / 1000)
    renderCacheView({
      enabled: true,
      trackerHitsSaved: 128,
      hitRatio: 0.75,
      entries: 12,
      windows: [
        { window: "1d", hits: 16, misses: 16, hitRatio: 0.5 },
        { window: "7d", hits: 40, misses: 20, hitRatio: 0.6 },
        { window: "30d", hits: 90, misses: 30, hitRatio: 0.7 },
        { window: "all", hits: 128, misses: 42, hitRatio: 0.75 },
      ],
      windowsSince: now - 7200, // two hours of buckets only
      byIndexer: [],
    })

    // Defaults to All: the tiles read exactly as they did before the picker landed.
    expect(await screen.findByText("128")).toBeTruthy()
    expect(screen.getByText("75%")).toBeTruthy()
    // All-time is persisted, so it carries no coverage caveat.
    expect(screen.queryByText(/only collecting since/)).toBeNull()

    fireEvent.click(screen.getByText("7d"))
    expect(screen.getByText("40")).toBeTruthy()
    expect(screen.getByText("60%")).toBeTruthy()
    // Two hours of buckets cannot back a 7d view — say so rather than imply a week.
    expect(screen.getByText("in-memory window — only collecting since 2h ago")).toBeTruthy()

    fireEvent.click(screen.getByText("24h"))
    expect(screen.getByText("16")).toBeTruthy()
    expect(screen.getByText("50%")).toBeTruthy()
  })

  it("renders an em dash for every age when the cache is empty", async () => {
    renderCacheView({
      enabled: true,
      entries: 0,
      oldestCachedAt: null,
      newestCachedAt: null,
      lastUsedAt: null,
      byIndexer: [],
    })

    expect(await screen.findByText("Oldest entry — · newest — · last served —")).toBeTruthy()
  })
})
