import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { fireEvent, render, screen } from "@testing-library/react"
import { afterEach, describe, expect, it, vi } from "vitest"
import { DashboardTiles } from "./DashboardTiles"

const INDEXERS = [
  { id: 1, slug: "a", definitionId: "a", name: "A", enabled: true, createdAt: "2026-07-01T00:00:00Z", updatedAt: "2026-07-01T00:00:00Z" },
  { id: 2, slug: "b", definitionId: "b", name: "B", enabled: true, createdAt: "2026-07-01T00:00:00Z", updatedAt: "2026-07-01T00:00:00Z" },
]

function json(body: unknown): Response {
  return new Response(JSON.stringify(body), { status: 200, headers: { "Content-Type": "application/json" } })
}

describe("DashboardTiles", () => {
  afterEach(() => vi.unstubAllGlobals())

  it("renders health, cache, connection, and breaker tiles from the APIs", async () => {
    vi.stubGlobal("fetch", vi.fn().mockImplementation((request: Request) => {
      const path = request.url
      if (path.endsWith("/api/indexers")) return Promise.resolve(json(INDEXERS))
      if (path.includes("/status")) {
        const slug = path.includes("/a/") ? "a" : "b"
        return Promise.resolve(json({
          slug,
          status: slug === "a" ? "healthy" : "unhealthy",
          events: [],
        }))
      }
      if (path.endsWith("/api/cache/stats")) {
        return Promise.resolve(json({
          enabled: true,
          trackerHitsSaved: 128,
          hitRatio: 0.75,
          hits24h: 16,
          hitRatio24h: 0.5,
          byIndexer: [{ instanceId: 2, slug: "b", breakerOpenUntil: 1_900_000_000 }],
        }))
      }
      if (path.endsWith("/api/app-connections")) return Promise.resolve(json([]))
      return Promise.resolve(json({}))
    }))

    render(
      <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
        <DashboardTiles />
      </QueryClientProvider>
    )

    expect(await screen.findByText("1/2")).toBeTruthy() // healthy/total
    expect(await screen.findByText("128")).toBeTruthy() // trackerHitsSaved
    expect(screen.getByText("75% hit ratio · lifetime")).toBeTruthy()
    expect(await screen.findByText("Circuit breakers open")).toBeTruthy()
    expect(screen.getByText("1")).toBeTruthy() // one open breaker

    // Clicking the cache tile switches to the rolling-24h window and back.
    fireEvent.click(screen.getByText("Tracker hits saved"))
    expect(screen.getByText("Tracker hits saved (24h)")).toBeTruthy()
    expect(screen.getByText("16")).toBeTruthy() // hits24h
    expect(screen.getByText("50% hit ratio · 24h")).toBeTruthy()
    fireEvent.click(screen.getByText("Tracker hits saved (24h)"))
    expect(screen.getByText("128")).toBeTruthy()
  })
})
