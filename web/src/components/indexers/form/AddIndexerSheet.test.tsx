import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { render, screen } from "@testing-library/react"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"
import type { DefinitionEntry } from "@/lib/api"
import { IndexerSheet } from "./AddIndexerSheet"

// More loadable definitions than the picker's 50-row cap, plus one that failed
// to load. The failed id sorts LAST alphabetically, so it only appears if the
// picker hoists failures above the cap rather than letting them fall off the
// bottom of a long catalog.
const LOADABLE: DefinitionEntry[] = Array.from({ length: 60 }, (_, i) => ({
  id: `tracker-${String(i).padStart(2, "0")}`,
  name: `Tracker ${i}`,
  type: "private",
}))

const FAILED: DefinitionEntry = {
  id: "zzz-broken",
  name: "zzz-broken",
  origin: "dropin",
  error: "loading drop-in definition zzz-broken: schema validation failed at: /search/fields/title",
}

function json(body: unknown): Response {
  return new Response(JSON.stringify(body), { status: 200, headers: { "Content-Type": "application/json" } })
}

beforeEach(() => {
  vi.stubGlobal("fetch", vi.fn((request: Request) => {
    if (request.url.endsWith("/api/definitions")) return Promise.resolve(json([...LOADABLE, FAILED]))
    return Promise.resolve(json([]))
  }))
})
afterEach(() => vi.unstubAllGlobals())

function renderCreateFlow() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={qc}>
      <IndexerSheet state={{ open: true, mode: "create" }} onClose={() => {}} />
    </QueryClientProvider>
  )
}

describe("IndexerSheet create flow", () => {
  it("shows a failed definition even when the catalog overflows the row cap", async () => {
    renderCreateFlow()

    expect(await screen.findByText("zzz-broken")).toBeTruthy()
    expect(screen.getByText("dropin")).toBeTruthy()
    // The last loadable definition is the one pushed off the cap, confirming the
    // list really is truncated and the failure was hoisted past it.
    expect(screen.queryByText("Tracker 59")).toBeNull()
  })

  it("counts only loadable definitions as available", async () => {
    renderCreateFlow()

    expect(await screen.findByText(/60 available/)).toBeTruthy()
  })
})
