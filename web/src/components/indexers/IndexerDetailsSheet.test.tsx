import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { render, screen } from "@testing-library/react"
import { afterEach, describe, expect, it, vi } from "vitest"
import { IndexerDetailsSheet } from "./IndexerDetailsSheet"

function json(body: unknown): Response {
  return new Response(JSON.stringify(body), { status: 200, headers: { "Content-Type": "application/json" } })
}

describe("IndexerDetailsSheet", () => {
  afterEach(() => vi.unstubAllGlobals())

  it("renders the summed failure count from the per-kind object without crashing", async () => {
    vi.stubGlobal("fetch", vi.fn().mockImplementation((request: Request) => {
      const path = request.url
      if (path.includes("/stats")) {
        return Promise.resolve(json({
          slug: "torrentleech",
          queries: 10,
          grabs: 2,
          avgResponseMs: 120,
          failures: { authFailure: 1, rateLimited: 2, parseError: 0, antiBot: 3 },
        }))
      }
      if (path.includes("/status")) {
        return Promise.resolve(json({ slug: "torrentleech", status: "healthy", events: [] }))
      }
      if (path.includes("/capabilities")) {
        return Promise.resolve(json({ modes: { search: ["q"] } }))
      }
      return Promise.resolve(json({}))
    }))

    render(
      <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
        <IndexerDetailsSheet slug="torrentleech" onClose={vi.fn()} />
      </QueryClientProvider>
    )

    // 1 + 2 + 0 + 3 = 6. If the object were rendered directly instead of the
    // sum, React would throw "Objects are not valid as a React child".
    expect(await screen.findByText("6")).toBeTruthy()
    // No grab attempts recorded: the bare count, never a "0%" success rate.
    expect(await screen.findByText("2")).toBeTruthy()
    expect(screen.queryByText("By category")).toBeNull()
  })

  it("renders the grab success rate and the per-category tallies", async () => {
    vi.stubGlobal("fetch", vi.fn().mockImplementation((request: Request) => {
      const path = request.url
      if (path.includes("/stats")) {
        return Promise.resolve(json({
          slug: "torrentleech",
          queries: 10,
          grabAttempts: 6,
          grabs: 5,
          grabSuccessRate: 5 / 6,
          avgResponseMs: 120,
          failures: { authFailure: 0, rateLimited: 0, parseError: 0, antiBot: 0 },
          categories: [
            { id: 2000, name: "Movies", results: 40, grabs: 4 },
            { id: 0, name: "Uncategorized", results: 2, grabs: 1 },
          ],
        }))
      }
      if (path.includes("/status")) {
        return Promise.resolve(json({ slug: "torrentleech", status: "healthy", events: [] }))
      }
      if (path.includes("/capabilities")) {
        return Promise.resolve(json({ modes: { search: ["q"] } }))
      }
      return Promise.resolve(json({}))
    }))

    render(
      <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
        <IndexerDetailsSheet slug="torrentleech" onClose={vi.fn()} />
      </QueryClientProvider>
    )

    expect(await screen.findByText("5 · 1 failed (83%)")).toBeTruthy()
    expect(await screen.findByText("Movies")).toBeTruthy()
    expect(await screen.findByText("Uncategorized")).toBeTruthy()
    expect(await screen.findByText("40")).toBeTruthy()
  })
})
