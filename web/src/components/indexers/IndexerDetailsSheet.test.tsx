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
  })
})
