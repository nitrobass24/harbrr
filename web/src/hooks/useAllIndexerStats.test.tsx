import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { renderHook, waitFor } from "@testing-library/react"
import { beforeEach, describe, expect, it, vi } from "vitest"
import type { ReactNode } from "react"
import { useAllIndexerStats } from "./useSettings"
import { useIndexer } from "./useIndexers"

// Both queries here resolve to the SAME URL (/api/indexers/stats — that collision is
// the point of the test), so a fetch-level stub cannot tell them apart. Mock the typed
// client's GET at the template-path seam instead, where "/api/indexers/{slug}" and
// "/api/indexers/stats" are still distinct.
const { getMock } = vi.hoisted(() => ({
  getMock: vi.fn(),
}))
vi.mock("@/lib/api", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@/lib/api")>()),
  api: { http: { GET: getMock } },
}))

function wrapper(qc: QueryClient) {
  return ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={qc}>{children}</QueryClientProvider>
  )
}

describe("useAllIndexerStats query-key isolation (U14-F3)", () => {
  beforeEach(() => {
    getMock.mockReset()
  })

  // An indexer slugged "stats" and the aggregate stats list must NOT share a
  // TanStack Query cache entry. Before the fix both used ["indexers", "stats"];
  // useAllIndexerStats now lives under ["indexer-stats"], so the detail query and
  // the aggregate query resolve to their own distinct data.
  it("does not collide with the per-indexer detail key for slug 'stats'", async () => {
    const detail = { instance: { slug: "stats" }, settings: [] }
    const aggregate = [{ slug: "a", queries: 1, grabs: 0, avgResponseMs: 0 }]
    getMock.mockImplementation((path: string) =>
      Promise.resolve({ data: path === "/api/indexers/stats" ? aggregate : detail, response: Response.json(null) }))

    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    const detailHook = renderHook(() => useIndexer("stats"), { wrapper: wrapper(qc) })
    const aggHook = renderHook(() => useAllIndexerStats(), { wrapper: wrapper(qc) })

    await waitFor(() => expect(detailHook.result.current.data).toEqual(detail))
    await waitFor(() => expect(aggHook.result.current.data).toEqual(aggregate))

    // Each hook keeps its own value — no cross-poisoning of one cache entry.
    expect(detailHook.result.current.data).toEqual(detail)
    expect(aggHook.result.current.data).toEqual(aggregate)
    // The aggregate is keyed off its own root, not under ["indexers", ...].
    expect(qc.getQueryData(["indexer-stats"])).toEqual(aggregate)
    expect(qc.getQueryData(["indexers", "stats"])).toEqual(detail)
  })
})
