import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { fireEvent, render, screen, waitFor } from "@testing-library/react"
import { createMemoryHistory, createRouter, RouterProvider } from "@tanstack/react-router"
import { afterEach, describe, expect, it, vi } from "vitest"
import { ThemeProvider } from "@/components/themes/theme-provider"
import { routeTree } from "@/routeTree.gen"

const ME = { username: "admin", authMethod: "password", csrfToken: "tok" }

const INDEXER = {
  id: 1, slug: "demotracker", definitionId: "demo", name: "Demo", enabled: true,
  protocol: "torrent", createdAt: "2026-07-01T00:00:00Z", updatedAt: "2026-07-01T00:00:00Z",
}

const CAPS = { categories: [{ id: 2000, name: "Movies" }, { id: 5000, name: "TV" }], modes: {} }

const RESULTS = {
  results: [
    { title: "Big Buck Bunny 2160p x265", categories: [2000], seeders: 9, size: 100 },
    { title: "Sintel S01E02 1080p x264", categories: [5000], seeders: 5, size: 100 },
    { title: "Tears of Steel 1080p x265", categories: [2000], seeders: 1, size: 100 },
  ],
  hasMore: false,
}

function json(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } })
}

function stubFetch() {
  const fetchMock = vi.fn((request: Request) => {
    const url = request.url
    if (url.endsWith("/auth/me")) return Promise.resolve(json(ME))
    if (url.includes("/capabilities")) return Promise.resolve(json(CAPS))
    if (url.includes("/search")) return Promise.resolve(json(RESULTS))
    if (url.includes("/api/indexers")) return Promise.resolve(json([INDEXER]))
    return Promise.resolve(json([]))
  })
  vi.stubGlobal("fetch", fetchMock)
  return fetchMock
}

function renderSearch() {
  const router = createRouter({ routeTree, history: createMemoryHistory({ initialEntries: ["/search"] }) })
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  render(
    <QueryClientProvider client={queryClient}>
      <ThemeProvider>
        <RouterProvider router={router} />
      </ThemeProvider>
    </QueryClientProvider>
  )
}

// Runs a search so the page holds the three RESULTS rows, then hands back the filter
// input and how many fetches the search itself cost.
async function searchAndFilter() {
  const fetchMock = stubFetch()
  renderSearch()

  const button = await screen.findByRole<HTMLButtonElement>("button", { name: "Search" })
  await waitFor(() => expect(button.disabled).toBe(false))
  fireEvent.click(button)
  await screen.findByText("3 results · page 1")

  return { filter: screen.getByLabelText("Filter results"), fetchesAfterSearch: fetchMock.mock.calls.length, fetchMock }
}

const type = (input: HTMLElement, value: string) => fireEvent.change(input, { target: { value } })

describe("Search route — result filter (autobrr/harbrr#374)", () => {
  afterEach(() => vi.unstubAllGlobals())

  it("narrows rows to a substring match and shows N of M without re-querying", async () => {
    const { filter, fetchesAfterSearch, fetchMock } = await searchAndFilter()

    type(filter, "x265")

    await screen.findByText("2 of 3 results · page 1")
    expect(screen.queryByText("Sintel S01E02 1080p x264")).toBeNull()
    expect(screen.getByText("Big Buck Bunny 2160p x265")).toBeTruthy()
    // The whole point: typing filters state, it never hits the network.
    expect(fetchMock.mock.calls.length).toBe(fetchesAfterSearch)
  })

  it("excludes with a leading - and matches a /regex/", async () => {
    const { filter } = await searchAndFilter()

    type(filter, "-x265")
    await screen.findByText("1 of 3 results · page 1")
    expect(screen.getByText("Sintel S01E02 1080p x264")).toBeTruthy()

    type(filter, String.raw`/S\d\dE\d\d/`)
    await screen.findByText("1 of 3 results · page 1")
    expect(screen.getByText("Sintel S01E02 1080p x264")).toBeTruthy()
  })

  it("keeps the last valid view and flags a half-typed pattern", async () => {
    const { filter } = await searchAndFilter()

    type(filter, "x265")
    await screen.findByText("2 of 3 results · page 1")

    type(filter, "x265 /[")
    await screen.findByText(/Invalid pattern/)
    // Still the x265 view — an in-flight character never blanks the results.
    expect(screen.getByText("2 of 3 results · page 1")).toBeTruthy()
  })

  it("Escape clears the filter and restores every row", async () => {
    const { filter } = await searchAndFilter()

    type(filter, "sintel")
    await screen.findByText("1 of 3 results · page 1")

    fireEvent.keyDown(filter, { key: "Escape" })
    await screen.findByText("3 results · page 1")
    expect(screen.getByText("Big Buck Bunny 2160p x265")).toBeTruthy()
  })
})
