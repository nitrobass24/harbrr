import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { act, renderHook, waitFor } from "@testing-library/react"
import type { ReactNode } from "react"
import { describe, expect, it } from "vitest"
import { APIError } from "@/lib/api"
import { useTestAllIndexers } from "./useIndexers"
import { stubApi } from "@/test/stubApi"

// Test all used to fire one request per enabled indexer through an unbounded Promise.all
// (autobrr/harbrr#485): on a 22-indexer fleet that is 22 concurrent logins from one click,
// and the test path deliberately bypasses the circuit breaker and the request budget, so
// nothing else was throttling it.

function wrapper({ children }: { children: ReactNode }) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return <QueryClientProvider client={qc}>{children}</QueryClientProvider>
}

describe("useTestAllIndexers", () => {
  it("never exceeds the concurrency cap, and still returns every result in order", async () => {
    const slugs = Array.from({ length: 22 }, (_, i) => `ix${i}`)
    const release: Array<() => void> = []
    let inFlight = 0
    let peak = 0

    // Three shapes, because the pool handles them differently. A resolved failure is the
    // response contract; a THROWN one (a rejected transport, here at the stubbed fetch
    // seam) has to be caught inside the worker or it rejects that worker, which rejects
    // the whole batch and takes the other 21 results with it.
    stubApi({
      "POST /api/indexers/{slug}/test": (request: Request) => {
        const slug = new URL(request.url).pathname.split("/").at(-2) as string
        inFlight++
        peak = Math.max(peak, inFlight)
        return new Promise<Response>((resolve, reject) => {
          release.push(() => {
            inFlight--
            if (slug === "ix7") return reject(new APIError(401, "unauthorized", "session expired"))
            if (slug === "ix11") return reject(new Error("network down"))
            resolve(Response.json({ ok: slug !== "ix3", error: slug === "ix3" ? "bad passkey" : undefined }))
          })
        })
      },
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
    // A failing indexer neither aborts the batch nor masks the others — resolved or thrown.
    expect(results.find((r) => r.slug === "ix3")).toMatchObject({ ok: false, error: "bad passkey" })
    // An APIError keeps its status so the caller can tell 401/403 from a tracker failure.
    expect(results.find((r) => r.slug === "ix7")).toMatchObject({
      ok: false, error: "session expired", status: 401,
    })
    // Anything else falls back to the fixed message rather than leaking the raw throw.
    expect(results.find((r) => r.slug === "ix11")).toMatchObject({ ok: false, error: "test request failed" })
    expect(results.filter((r) => r.ok)).toHaveLength(19)
  })
})
