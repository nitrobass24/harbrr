import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { fireEvent, render, screen, waitFor, within } from "@testing-library/react"
import { afterEach, describe, expect, it, vi } from "vitest"
import type { ReactNode } from "react"
import { SyncProfilesSection } from "./SyncProfilesSection"

const CREATED = {
  id: 1,
  name: "tv-profile",
  categories: [2000, 3030, 5000],
  minSeeders: 0,
  enableRss: true,
  enableAutomaticSearch: true,
  enableInteractiveSearch: true,
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
    return Promise.resolve(jsonResponse([]))
  })
  vi.stubGlobal("fetch", fetchMock)
  return fetchMock
}

describe("SyncProfilesSection", () => {
  afterEach(() => vi.unstubAllGlobals())

  it("renders the stubbed list", async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse([CREATED]))
    vi.stubGlobal("fetch", fetchMock)
    render(wrap(<SyncProfilesSection />))

    expect(await screen.findByText("tv-profile")).toBeTruthy()
    expect(screen.getByText(/3 categories/)).toBeTruthy()
  })

  it("adding a profile: checking TV + Movies and typing extras submits the sorted, deduped category list", async () => {
    const fetchMock = stubFetch()
    render(wrap(<SyncProfilesSection />))

    fireEvent.click(screen.getByRole("button", { name: /Add profile/ }))
    const dialog = await screen.findByRole("dialog")

    fireEvent.change(within(dialog).getByLabelText("Name"), { target: { value: "tv-profile" } })
    fireEvent.click(within(dialog).getByLabelText("TV"))
    fireEvent.click(within(dialog).getByLabelText("Movies"))
    // Garbage in the extras field (text, negatives, decimals) is dropped — only the
    // positive integer 3030 survives into the body (see the submit-handler filter).
    fireEvent.change(within(dialog).getByPlaceholderText(/Extra category IDs/), { target: { value: "abc, -5, 3030.5, 3030" } })

    fireEvent.click(within(dialog).getByRole("button", { name: "Add profile" }))

    // The dialog closes on a successful create — wait for that before inspecting the request.
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull())
    const postCall = fetchMock.mock.calls.find(([request]) => (request as Request).method === "POST")
    expect(postCall).toBeTruthy()
    const body: unknown = JSON.parse(await (postCall![0] as Request).text())
    expect(body).toEqual({
      name: "tv-profile",
      categories: [2000, 3030, 5000],
      minSeeders: 0,
      enableRss: true,
      enableAutomaticSearch: true,
      enableInteractiveSearch: true,
    })
  })
})
