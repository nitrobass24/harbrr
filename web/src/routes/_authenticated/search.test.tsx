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

const OTHER = { ...INDEXER, id: 2, slug: "ptp", name: "PTP" }

const CAPS = { categories: [{ id: 2000, name: "Movies" }, { id: 5000, name: "TV" }], modes: {} }

// The server-merged window: releases in server order, each naming its origin, plus the
// per-member ledger and the total the server stands behind (autobrr/harbrr#372).
const RESULTS = {
  // publishDate is deliberately NOT in array order: without dates the default age sort
  // is a no-op and "renders server order" passes vacuously (review finding). Newest is
  // Big Buck Bunny, and it is served LAST — so seeing it first proves the sort ran.
  results: [
    { indexer: "demotracker", release: { title: "Sintel S01E02 1080p x264", categories: [5000], seeders: 5, size: 100, publishDate: "2026-07-01T00:00:00Z" } },
    { indexer: "demotracker", release: { title: "Tears of Steel 1080p x265", categories: [2000], seeders: 1, size: 100, publishDate: "2026-07-10T00:00:00Z" } },
    { indexer: "demotracker", release: { title: "Big Buck Bunny 2160p x265", categories: [2000], seeders: 9, size: 100, publishDate: "2026-07-20T00:00:00Z" } },
  ],
  members: [{ slug: "demotracker", name: "Demo", status: "ok", count: 3 }],
  total: 3,
  limit: 100,
  offset: 0,
}

function json(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } })
}

// stubFetch serves the route's three reads: the auth probe, the indexer list (+ its
// capabilities) and the aggregate search. search is a thunk so a test can hold the
// search in flight; indexers sets how many the picker offers.
function stubFetch(search: () => Promise<Response> = () => Promise.resolve(json(RESULTS)), indexers: unknown[] = [INDEXER]) {
  const fetchMock = vi.fn((request: Request) => {
    const url = request.url
    if (url.endsWith("/auth/me")) return Promise.resolve(json(ME))
    if (url.includes("/capabilities")) return Promise.resolve(json(CAPS))
    if (url.includes("/api/search")) return search()
    if (url.includes("/api/indexers")) return Promise.resolve(json(indexers))
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

// submitSearch renders the page and clicks Search once the indexer list has landed.
async function submitSearch(fetchMock: ReturnType<typeof stubFetch>) {
  renderSearch()
  const button = await screen.findByRole<HTMLButtonElement>("button", { name: "Search" })
  await waitFor(() => expect(button.disabled).toBe(false))
  fireEvent.click(button)
  return fetchMock
}

// Runs a search so the page holds the three RESULTS rows, then hands back the filter
// input and how many fetches the search itself cost.
async function searchAndFilter() {
  const fetchMock = await submitSearch(stubFetch())
  await screen.findByText("3 results")

  return { filter: screen.getByLabelText("Filter results"), fetchesAfterSearch: fetchMock.mock.calls.length, fetchMock }
}

const type = (input: HTMLElement, value: string) => fireEvent.change(input, { target: { value } })

const searchURLs = (fetchMock: ReturnType<typeof stubFetch>) =>
  fetchMock.mock.calls.map(([req]) => req.url).filter((u) => u.includes("search"))

describe("Search route — result filter (autobrr/harbrr#374)", () => {
  afterEach(() => vi.unstubAllGlobals())

  it("narrows rows to a substring match and shows N of M without re-querying", async () => {
    const { filter, fetchesAfterSearch, fetchMock } = await searchAndFilter()

    type(filter, "x265")

    await screen.findByText("2 of 3 results")
    expect(screen.queryByText("Sintel S01E02 1080p x264")).toBeNull()
    expect(screen.getByText("Big Buck Bunny 2160p x265")).toBeTruthy()
    // The whole point: typing filters state, it never hits the network.
    expect(fetchMock.mock.calls.length).toBe(fetchesAfterSearch)
  })

  it("excludes with a leading - and matches a /regex/", async () => {
    const { filter } = await searchAndFilter()

    type(filter, "-x265")
    await screen.findByText("1 of 3 results")
    expect(screen.getByText("Sintel S01E02 1080p x264")).toBeTruthy()

    type(filter, String.raw`/S\d\dE\d\d/`)
    await screen.findByText("1 of 3 results")
    expect(screen.getByText("Sintel S01E02 1080p x264")).toBeTruthy()
  })

  it("keeps the last valid view and flags a half-typed pattern", async () => {
    const { filter } = await searchAndFilter()

    type(filter, "x265")
    await screen.findByText("2 of 3 results")

    type(filter, "x265 /[")
    await screen.findByText(/Invalid pattern/)
    // Still the x265 view — an in-flight character never blanks the results.
    expect(screen.getByText("2 of 3 results")).toBeTruthy()
  })

  it("Escape clears the filter and restores every row", async () => {
    const { filter } = await searchAndFilter()

    type(filter, "sintel")
    await screen.findByText("1 of 3 results")

    fireEvent.keyDown(filter, { key: "Escape" })
    await screen.findByText("3 results")
    expect(screen.getByText("Big Buck Bunny 2160p x265")).toBeTruthy()
  })
})

describe("Search route — server-merged aggregate (autobrr/harbrr#372)", () => {
  afterEach(() => vi.unstubAllGlobals())

  it("issues ONE aggregate request naming the subset, and no per-indexer searches", async () => {
    const fetchMock = await submitSearch(stubFetch(undefined, [INDEXER, OTHER]))
    await screen.findByText("3 results")

    const urls = searchURLs(fetchMock)
    expect(urls.length).toBe(1)
    expect(urls[0]).toContain("/api/search?")
    expect(decodeURIComponent(urls[0])).toContain("indexers=demotracker,ptp")
    // The client fan-out is gone: nothing may hit the per-indexer search route.
    expect(urls.some((u) => /\/api\/indexers\/[^/]+\/search/.test(u))).toBe(false)
  })

  it("folds answering indexers into one line and names each skip with its reason", async () => {
    const ledger = {
      ...RESULTS,
      members: [
        { slug: "demotracker", name: "Demo", status: "ok", count: 3 },
        { slug: "ptp", name: "PTP", status: "skipped", reason: "circuit-open", count: 0 },
        { slug: "dognzb", name: "DOGnzb", status: "skipped", reason: "budget-exhausted", count: 0 },
      ],
    }
    await submitSearch(stubFetch(() => Promise.resolve(json(ledger)), [INDEXER, OTHER]))

    await screen.findByText("1 indexer · 3 results")
    expect(screen.getByText(/PTP — circuit open/)).toBeTruthy()
    expect(screen.getByText(/DOGnzb — budget exhausted/)).toBeTruthy()
  })

  it("states the merged total the server stands behind when it exceeds the window", async () => {
    await submitSearch(stubFetch(() => Promise.resolve(json({ ...RESULTS, total: 412 }))))

    await screen.findByText("· newest 3 of 412 fetched")
  })

  it("has no paging controls — the merged window is one snapshot", async () => {
    await submitSearch(stubFetch())
    await screen.findByText("3 results")

    expect(screen.queryByRole("button", { name: "Next" })).toBeNull()
    expect(screen.queryByRole("button", { name: "Previous" })).toBeNull()
  })

  it("says how many indexers are being searched while the request is in flight", async () => {
    let release: (r: Response) => void = () => {}
    const pending = new Promise<Response>((resolve) => { release = resolve })
    await submitSearch(stubFetch(() => pending, [INDEXER, OTHER]))

    await screen.findByText("Searching 2 indexers…")
    release(json(RESULTS))
    await screen.findByText("3 results")
    expect(screen.queryByText("Searching 2 indexers…")).toBeNull()
  })

  it("sorts a column across the whole served window", async () => {
    await submitSearch(stubFetch())
    await screen.findByText("3 results")

    // Default order is publish-date desc (newest first). The fixture serves the newest
    // release LAST, so this only passes if the age sort actually ran over the window.
    const titles = () => screen.getAllByRole("row").slice(1).map((r) => r.textContent ?? "")
    expect(titles()[0]).toContain("Big Buck Bunny")
    expect(titles()[2]).toContain("Sintel")

    fireEvent.click(screen.getByRole("button", { name: /Title/ }))
    await waitFor(() => expect(titles()[0]).toContain("Tears of Steel")) // title desc
    expect(titles()[2]).toContain("Big Buck Bunny")
  })

  it("does not hang on Searching when the subset is empty", async () => {
    // A DISABLED react-query stays `pending` forever, so the searching flag must also
    // require a non-empty subset — otherwise an operator with nothing selected sees
    // "Searching 0 indexers…" indefinitely, with results and ledger unreachable
    // (review finding). No enabled indexers is the same empty-subset condition.
    stubFetch(() => Promise.resolve(json(RESULTS)), [])
    renderSearch()
    await screen.findByRole("button", { name: "Search" })
    await waitFor(() => expect(screen.queryByText(/Searching/)).toBeNull())
  })

  it("surfaces a failed search instead of rendering an empty result set", async () => {
    await submitSearch(stubFetch(() => Promise.resolve(json({ error: "no such indexer", code: "bad_request" }, 400))))

    await screen.findByText(/Search failed/)
    expect(screen.queryByText("No results.")).toBeNull()
  })
})
