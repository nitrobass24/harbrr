import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { act, renderHook, waitFor } from "@testing-library/react"
import type { ReactNode } from "react"
import { beforeEach, describe, expect, it, vi } from "vitest"
import { useTestAllIndexers } from "./useIndexers"

// Test all used to fire one request per enabled indexer through an unbounded Promise.all
// (autobrr/harbrr#485): on a 22-indexer fleet that is 22 concurrent logins from one click,
// and the test path deliberately bypasses the circuit breaker and the request budget, so
// nothing else was throttling it.
const { testIndexer } = vi.hoisted(() => ({ testIndexer: vi.fn() }))
vi.mock("@/lib/api", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@/lib/api")>()),
  api: { testIndexer },
}))

function wrapper({ children }: { children: ReactNode }) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return <QueryClientProvider client={qc}>{children}</QueryClientProvider>
}

describe("useTestAllIndexers", () => {
  beforeEach(() => {
    testIndexer.mockReset()
  })

  it("never exceeds the concurrency cap, and still returns every result in order", async () => {
    const slugs = Array.from({ length: 22 }, (_, i) => `ix${i}`)
    const release: Array<() => void> = []
    let inFlight = 0
    let peak = 0

    testIndexer.mockImplementation((slug: string) => {
      inFlight++
      peak = Math.max(peak, inFlight)
      return new Promise((resolve) => {
        release.push(() => {
          inFlight--
          resolve({ ok: slug !== "ix3", error: slug === "ix3" ? "bad passkey" : undefined })
        })
      })
    })

    const { result } = renderHook(() => useTestAllIndexers(), { wrapper })
    let done: Promise<Awaited<ReturnType<typeof result.current.mutateAsync>>>
    act(() => {
      done = result.current.mutateAsync(slugs)
    })

    // Only the cap starts; the 9th must wait for a slot rather than joining the burst.
    await waitFor(() => expect(release.length).toBe(8))
    expect(peak).toBe(8)

    // Drain, releasing as workers pick up more work.
    for (let i = 0; i < slugs.length; i++) {
      await waitFor(() => expect(release.length).toBeGreaterThan(i))
      act(() => release[i]())
    }

    const results = await done!
    expect(peak).toBe(8)
    expect(results).toHaveLength(22)
    expect(results.map((r) => r.slug)).toEqual(slugs)
    // One failing indexer neither aborts the batch nor masks the others.
    expect(results.find((r) => r.slug === "ix3")).toMatchObject({ ok: false, error: "bad passkey" })
    expect(results.filter((r) => r.ok)).toHaveLength(21)
  })
})
