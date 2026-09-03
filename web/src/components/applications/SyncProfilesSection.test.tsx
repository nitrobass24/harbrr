import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { fireEvent, render, screen, waitFor, within } from "@testing-library/react"
import { describe, expect, it } from "vitest"
import type { ReactNode } from "react"
import { stubApi } from "@/test/stubApi"
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

function wrap(children: ReactNode) {
  return (
    <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      {children}
    </QueryClientProvider>
  )
}

describe("SyncProfilesSection", () => {
  it("renders the stubbed list", async () => {
    stubApi({ "GET /api/sync-profiles": [CREATED] })
    render(wrap(<SyncProfilesSection />))

    expect(await screen.findByText("tv-only")).toBeTruthy()
    expect(screen.getByText("1 indexer")).toBeTruthy()
  })

  it("renders 'all indexers' for an empty selection", async () => {
    stubApi({ "GET /api/sync-profiles": [{ ...CREATED, indexerIds: [] }] })
    render(wrap(<SyncProfilesSection />))

    expect(await screen.findByText("all indexers")).toBeTruthy()
  })

  it("adding a profile: naming it and checking one indexer submits its id", async () => {
    const api = stubApi({
      "GET /api/sync-profiles": [],
      "GET /api/indexers": INDEXERS,
      "POST /api/sync-profiles": () => Response.json(CREATED, { status: 201 }),
    })
    render(wrap(<SyncProfilesSection />))

    fireEvent.click(screen.getByRole("button", { name: /Add profile/ }))
    const dialog = await screen.findByRole("dialog")

    fireEvent.change(within(dialog).getByLabelText("Name"), { target: { value: "tv-only" } })
    expect(await within(dialog).findByLabelText("TorrentTracker")).toBeTruthy()
    fireEvent.click(within(dialog).getByLabelText("TorrentTracker"))

    fireEvent.click(within(dialog).getByRole("button", { name: "Add profile" }))

    // The dialog closes on a successful create — wait for that before inspecting the request.
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull())
    const post = api.calls("POST /api/sync-profiles")[0]
    expect(post).toBeTruthy()
    const body: unknown = JSON.parse(await post.text())
    expect(body).toEqual({ name: "tv-only", indexerIds: [1] })
  })
})
