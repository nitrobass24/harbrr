import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { fireEvent, render, screen, waitFor } from "@testing-library/react"
import { createMemoryHistory, createRouter, RouterProvider } from "@tanstack/react-router"
import { describe, expect, it } from "vitest"
import { ThemeProvider } from "@/components/themes/theme-provider"
import { stubApi } from "@/test/stubApi"
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
// search in flight; indexers sets how many the picker offers. A per-indexer search
// (/api/indexers/{slug}/search) is deliberately NOT stubbed: any client fan-out
// would throw UnstubbedRequestError instead of passing silently.
function stubFetch(search: () => Promise<Response> = () => Promise.resolve(json(RESULTS)), indexers: unknown[] = [INDEXER]) {
  return stubApi({
    "GET /api/auth/me": ME,
    "GET /api/indexers/{slug}/capabilities": CAPS,
    "GET /api/search": () => search(),
    "GET /api/indexers": indexers,
  })
}

// Every key stubFetch stubs; anything else throws, so summing these IS the total
// number of requests the page made — the no-refetch assertions count with this.
const STUB_KEYS = ["GET /api/auth/me", "GET /api/indexers/{slug}/capabilities", "GET /api/search", "GET /api/indexers"]

const totalFetches = (api: ReturnType<typeof stubFetch>) =>
  STUB_KEYS.reduce((n, key) => n + api.calls(key).length, 0)

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
async function submitSearch(api: ReturnType<typeof stubFetch>) {
  renderSearch()
  const button = await screen.findByRole<HTMLButtonElement>("button", { name: "Search" })
  await waitFor(() => expect(button.disabled).toBe(false))
  fireEvent.click(button)
  return api
}

// Runs a search so the page holds the three RESULTS rows, then hands back the filter
// input and how many fetches the search itself cost.
async function searchAndFilter() {
  const api = await submitSearch(stubFetch())
  await screen.findByText("3 results")

  return { filter: screen.getByLabelText("Filter results"), fetchesAfterSearch: totalFetches(api), api }
}

const type = (input: HTMLElement, value: string) => fireEvent.change(input, { target: { value } })

describe("Search route — result filter (autobrr/harbrr#374)", () => {
  it("narrows rows to a substring match and shows N of M without re-querying", async () => {
    const { filter, fetchesAfterSearch, api } = await searchAndFilter()

    type(filter, "x265")

    await screen.findByText("2 of 3 results")
    expect(screen.queryByText("Sintel S01E02 1080p x264")).toBeNull()
    expect(screen.getByText("Big Buck Bunny 2160p x265")).toBeTruthy()
    // The whole point: typing filters state, it never hits the network.
    expect(totalFetches(api)).toBe(fetchesAfterSearch)
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
  it("issues ONE aggregate request naming the subset, and no per-indexer searches", async () => {
    const api = await submitSearch(stubFetch(undefined, [INDEXER, OTHER]))
    await screen.findByText("3 results")

    const searches = api.calls("GET /api/search")
    expect(searches.length).toBe(1)
    expect(searches[0].url).toContain("/api/search?")
    expect(decodeURIComponent(searches[0].url)).toContain("indexers=demotracker,ptp")
    // The client fan-out is gone: the per-indexer search route is unstubbed, so any
    // request to it would have thrown UnstubbedRequestError instead of resolving.
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

// The same Sintel release on both indexers (one infohash), plus two releases carried by
// one indexer each — so grouping folds 4 rows into 3 releases.
const CROSS_SEEDED = {
  ...RESULTS,
  results: [
    { indexer: "demotracker", release: { title: "Sintel S01E02 1080p x264", infohash: "ABCDEF", categories: [5000], seeders: 5, size: 100, publishDate: "2026-07-01T00:00:00Z" } },
    { indexer: "ptp", release: { title: "sintel.s01e02.1080p.x264", infohash: "abcdef", categories: [5000], seeders: 40, size: 100, publishDate: "2026-07-02T00:00:00Z" } },
    { indexer: "demotracker", release: { title: "Tears of Steel 1080p x265", categories: [2000], seeders: 1, size: 100, publishDate: "2026-07-10T00:00:00Z" } },
    { indexer: "ptp", release: { title: "Big Buck Bunny 2160p x265", categories: [2000], seeders: 9, size: 100, publishDate: "2026-07-20T00:00:00Z" } },
  ],
  members: [{ slug: "demotracker", name: "Demo", status: "ok", count: 2 }, { slug: "ptp", name: "PTP", status: "ok", count: 2 }],
  total: 4,
}

const rowText = () => screen.getAllByRole("row").slice(1).map((r) => r.textContent ?? "")

describe("Search route — result grouping (autobrr/harbrr#398)", () => {
  it("folds the same release from two trackers into one row, and toggles back to the flat list without re-querying", async () => {
    const api = await submitSearch(stubFetch(() => Promise.resolve(json(CROSS_SEEDED)), [INDEXER, OTHER]))
    await screen.findByText("4 results")

    // Grouped: 3 rows, the cross-seeded one badged with both trackers and its count.
    expect(rowText()).toHaveLength(3)
    expect(screen.getByText("· 3 releases")).toBeTruthy()
    const group = screen.getByRole("button", { name: /Expand .* 2 sources/ }).closest("tr")!
    expect(group.textContent).toContain("demotracker")
    expect(group.textContent).toContain("ptp")

    const fetchesAfterSearch = totalFetches(api)
    fireEvent.click(screen.getByRole("button", { name: "Group" }))

    // Off: today's flat list — every row back, in the sorted order, nothing merged.
    await waitFor(() => expect(rowText()).toHaveLength(4))
    expect(rowText().map((t) => t.slice(0, 30))).toEqual([
      expect.stringContaining("Big Buck Bunny"),
      expect.stringContaining("Tears of Steel"),
      expect.stringContaining("sintel.s01e02"),
      expect.stringContaining("Sintel S01E02"),
    ])
    expect(screen.queryByRole("button", { name: /2 sources/ })).toBeNull()
    expect(screen.getByText("4 results")).toBeTruthy()
    // A view mode, not a query: toggling either way never hits the network.
    expect(totalFetches(api)).toBe(fetchesAfterSearch)
  })

  it("expands a group to each tracker's own grabbable entry", async () => {
    await submitSearch(stubFetch(() => Promise.resolve(json(CROSS_SEEDED)), [INDEXER, OTHER]))
    await screen.findByText("4 results")

    fireEvent.click(screen.getByRole("button", { name: /Expand .* 2 sources/ }))

    await waitFor(() => expect(rowText()).toHaveLength(5))
    expect(screen.getByText("Sintel S01E02 1080p x264")).toBeTruthy()
    // The ptp member is the newest, so it is also the collapsed row's representative.
    expect(screen.getAllByText("sintel.s01e02.1080p.x264")).toHaveLength(2)
  })

  it("keeps a group whole when the filter matches only one of its members", async () => {
    await submitSearch(stubFetch(() => Promise.resolve(json(CROSS_SEEDED)), [INDEXER, OTHER]))
    await screen.findByText("4 results")

    type(screen.getByLabelText("Filter results"), "ptp")

    // Both of the cross-seeded rows count, though only the ptp one matched.
    await screen.findByText("3 of 4 results")
    expect(screen.getByRole("button", { name: /Expand .* 2 sources/ })).toBeTruthy()
  })
})
