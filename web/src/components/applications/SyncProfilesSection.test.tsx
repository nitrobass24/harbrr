import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { fireEvent, render, screen, waitFor, within } from "@testing-library/react"
import { afterEach, describe, expect, it, vi } from "vitest"
import type { ReactNode } from "react"
import { SyncProfilesSection } from "./SyncProfilesSection"

const INDEXERS = [
  { id: 1, slug: "tt", definitionId: "tt", name: "TorrentTracker", enabled: true, protocol: "torrent", freeleech: false, priority: 25, minSeeders: 0, syncCategories: [], enableRss: true, enableAutomaticSearch: true, enableInteractiveSearch: true, createdAt: "2026-07-03T00:00:00Z", updatedAt: "2026-07-03T00:00:00Z" },
  { id: 2, slug: "nn", definitionId: "nn", name: "NewzNab", enabled: true, protocol: "usenet", freeleech: false, priority: 25, minSeeders: 0, syncCategories: [], enableRss: true, enableAutomaticSearch: true, enableInteractiveSearch: true, createdAt: "2026-07-03T00:00:00Z", updatedAt: "2026-07-03T00:00:00Z" },
]

const CREATED = {
  id: 1,
  name: "tv-only",
  indexerIds: [1],
  createdAt: "2026-07-03T00:00:00Z",
  updatedAt: "2026-07-03T00:00:00Z",
}

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } })
}

function wrap(children: ReactNode) {
  return (
    <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      {children}
    </QueryClientProvider>
  )
}

function stubFetch() {
  const fetchMock = vi.fn().mockImplementation((request: Request) => {
    if (request.method === "POST" && request.url.endsWith("/sync-profiles")) {
      return Promise.resolve(jsonResponse(CREATED, 201))
    }
    if (request.url.endsWith("/indexers")) {
      return Promise.resolve(jsonResponse(INDEXERS))
    }
    return Promise.resolve(jsonResponse([]))
  })
  vi.stubGlobal("fetch", fetchMock)
  return fetchMock
}

describe("SyncProfilesSection", () => {
  afterEach(() => vi.unstubAllGlobals())

  it("renders the stubbed list", async () => {
    const fetchMock = vi.fn().mockImplementation((request: Request) => {
      if (request.url.endsWith("/sync-profiles")) return Promise.resolve(jsonResponse([CREATED]))
      return Promise.resolve(jsonResponse([]))
    })
    vi.stubGlobal("fetch", fetchMock)
    render(wrap(<SyncProfilesSection />))

    expect(await screen.findByText("tv-only")).toBeTruthy()
    expect(screen.getByText("1 indexer")).toBeTruthy()
  })

  it("renders 'all indexers' for an empty selection", async () => {
    const fetchMock = vi.fn().mockImplementation((request: Request) => {
      if (request.url.endsWith("/sync-profiles")) return Promise.resolve(jsonResponse([{ ...CREATED, indexerIds: [] }]))
      return Promise.resolve(jsonResponse([]))
    })
    vi.stubGlobal("fetch", fetchMock)
    render(wrap(<SyncProfilesSection />))

    expect(await screen.findByText("all indexers")).toBeTruthy()
  })

  it("adding a profile: naming it and checking one indexer submits its id", async () => {
    const fetchMock = stubFetch()
    render(wrap(<SyncProfilesSection />))

    fireEvent.click(screen.getByRole("button", { name: /Add profile/ }))
    const dialog = await screen.findByRole("dialog")

    fireEvent.change(within(dialog).getByLabelText("Name"), { target: { value: "tv-only" } })
    expect(await within(dialog).findByLabelText("TorrentTracker")).toBeTruthy()
    fireEvent.click(within(dialog).getByLabelText("TorrentTracker"))

    fireEvent.click(within(dialog).getByRole("button", { name: "Add profile" }))

    // The dialog closes on a successful create — wait for that before inspecting the request.
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull())
    const postCall = fetchMock.mock.calls.find(([request]) => (request as Request).method === "POST" && (request as Request).url.endsWith("/sync-profiles"))
    expect(postCall).toBeTruthy()
    const body: unknown = JSON.parse(await (postCall![0] as Request).text())
    expect(body).toEqual({ name: "tv-only", indexerIds: [1] })
  })
})
