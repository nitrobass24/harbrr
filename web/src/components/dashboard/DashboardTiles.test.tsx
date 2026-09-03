import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { fireEvent, render, screen } from "@testing-library/react"
import { describe, expect, it } from "vitest"
import { stubApi } from "@/test/stubApi"
import { DashboardTiles } from "./DashboardTiles"

const INDEXERS = [
  { id: 1, slug: "a", definitionId: "a", name: "A", enabled: true, createdAt: "2026-07-01T00:00:00Z", updatedAt: "2026-07-01T00:00:00Z" },
  { id: 2, slug: "b", definitionId: "b", name: "B", enabled: true, createdAt: "2026-07-01T00:00:00Z", updatedAt: "2026-07-01T00:00:00Z" },
]

describe("DashboardTiles", () => {
  it("renders health, cache, connection, and breaker tiles from the APIs", async () => {
    stubApi({
      "GET /api/indexers": INDEXERS,
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
        trackerHitsSaved: 128,
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

    render(
      <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
        <DashboardTiles />
      </QueryClientProvider>
    )

    expect(await screen.findByText("1/2")).toBeTruthy() // healthy/total
    expect(screen.getByText("1 failing")).toBeTruthy() // the tri-state remainder (#389)
    expect(await screen.findByText("128")).toBeTruthy() // trackerHitsSaved
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
})
