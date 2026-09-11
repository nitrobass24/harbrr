import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { fireEvent, render, screen, within } from "@testing-library/react"
import { describe, expect, it } from "vitest"
import { withMemoryRouter } from "@/test/router"
import { stubApi } from "@/test/stubApi"
import { DashboardTiles } from "./DashboardTiles"

const INDEXERS = [
  { id: 1, slug: "a", definitionId: "a", name: "A", enabled: true, createdAt: "2026-07-01T00:00:00Z", updatedAt: "2026-07-01T00:00:00Z" },
  { id: 2, slug: "b", definitionId: "b", name: "B", enabled: true, createdAt: "2026-07-01T00:00:00Z", updatedAt: "2026-07-01T00:00:00Z" },
]

// Usage stats back the idle tile (#487). Both fixtures are idle here: one was never
// queried, the other has been quiet for a month.
const FAILURES = { authFailure: 0, rateLimited: 0, parseError: 0, antiBot: 0, transport: 0 }
const DAY = 24 * 60 * 60 * 1000
function stat(slug: string, queries: number, lastQueryAt?: string) {
  return { slug, queries, grabAttempts: 0, grabs: 0, avgResponseMs: 120, failures: FAILURES, categories: [], lastQueryAt }
}
const IDLE_STATS = [stat("a", 0), stat("b", 9, new Date(Date.now() - 30 * DAY).toISOString())]

describe("DashboardTiles", () => {
  it("renders health, cache, connection, and breaker tiles from the APIs", async () => {
    stubApi({
      "GET /api/indexers": INDEXERS,
      "GET /api/indexers/stats": IDLE_STATS,
      "GET /api/indexers/{slug}/status": (request: Request) => {
        const slug = request.url.endsWith("/a/status") ? "a" : "b"
        return Response.json({
          slug,
          status: slug === "a" ? "healthy" : "failing",
          events: [],
        })
      },
      "GET /api/cache/stats": {
        enabled: true,
        hits: 128,
        hitRatio: 0.75,
        windows: [
          { window: "1d", hits: 16, misses: 16, hitRatio: 0.5 },
          { window: "7d", hits: 40, misses: 20, hitRatio: 0.66 },
          { window: "30d", hits: 90, misses: 30, hitRatio: 0.75 },
          { window: "all", hits: 128, misses: 42, hitRatio: 0.75 },
        ],
        // A month of coverage: the 24h view is fully backed, so no caveat shows.
        windowsSince: Math.floor(Date.now() / 1000) - 30 * 24 * 3600,
        byIndexer: [{ instanceId: 2, slug: "b", breakerOpenUntil: 1_900_000_000 }],
      },
      "GET /api/app-connections": [],
    })

    render(withMemoryRouter(
      <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
        <DashboardTiles />
      </QueryClientProvider>
    ))

    expect(await screen.findByText("1/2")).toBeTruthy() // healthy/total
    expect(screen.getByText("1 failing")).toBeTruthy() // the tri-state remainder (#389)
    expect(await screen.findByText("128")).toBeTruthy() // hits
    expect(screen.getByText("75% hit ratio · lifetime")).toBeTruthy()
    expect(await screen.findByText("Circuit breakers open")).toBeTruthy()
    expect(screen.getByText("1")).toBeTruthy() // one open breaker

    // Clicking the cache tile switches to the rolling-24h window and back.
    fireEvent.click(screen.getByText("Tracker hits saved"))
    expect(screen.getByText("Tracker hits saved (24h)")).toBeTruthy()
    expect(screen.getByText("16")).toBeTruthy() // the 1d window's hits
    expect(screen.getByText("50% hit ratio · 24h")).toBeTruthy()
    fireEvent.click(screen.getByText("Tracker hits saved (24h)"))
    expect(screen.getByText("128")).toBeTruthy()
  })

  it("counts never-queried and long-quiet indexers on the idle tile (autobrr/harbrr#487)", async () => {
    stubApi({
      "GET /api/indexers": INDEXERS,
      "GET /api/indexers/stats": IDLE_STATS,
      "GET /api/indexers/{slug}/status": () => Response.json({ slug: "a", status: "healthy", events: [] }),
      "GET /api/cache/stats": { enabled: true, hits: 0, hitRatio: 0, windows: [], byIndexer: [] },
      "GET /api/app-connections": [],
    })

    render(withMemoryRouter(
      <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
        <DashboardTiles />
      </QueryClientProvider>
    ))

    const tile = (await screen.findByText("Idle indexers")).closest("a")!
    expect(await within(tile).findByText("2")).toBeTruthy()
    expect(within(tile).getByText("never queried or quiet 7d+")).toBeTruthy()
    // It is a way in, not just a number.
    expect(tile.getAttribute("href")).toContain("/indexers")
  })

  it("reports no idle indexers when everything is being queried", async () => {
    const recent = new Date(Date.now() - 2 * DAY).toISOString()
    stubApi({
      "GET /api/indexers": INDEXERS,
      "GET /api/indexers/stats": [stat("a", 5, recent), stat("b", 50, recent)],
      "GET /api/indexers/{slug}/status": () => Response.json({ slug: "a", status: "healthy", events: [] }),
      "GET /api/cache/stats": { enabled: true, hits: 0, hitRatio: 0, windows: [], byIndexer: [] },
      "GET /api/app-connections": [],
    })

    render(withMemoryRouter(
      <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
        <DashboardTiles />
      </QueryClientProvider>
    ))

    const tile = (await screen.findByText("Idle indexers")).closest("a")!
    expect(await within(tile).findByText("0")).toBeTruthy()
  })
})
