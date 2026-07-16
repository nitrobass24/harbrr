import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { render, screen, waitFor } from "@testing-library/react"
import { createMemoryHistory, createRouter, RouterProvider } from "@tanstack/react-router"
import { afterEach, describe, expect, it, vi } from "vitest"
import { ThemeProvider } from "@/components/themes/theme-provider"
import { routeTree } from "@/routeTree.gen"

const ME = { username: "admin", authMethod: "password", csrfToken: "tok" }

const INDEXER = {
  id: 1,
  slug: "torrentleech",
  definitionId: "torrentleech",
  name: "TorrentLeech",
  baseUrl: "https://www.torrentleech.org/",
  enabled: true,
  proxyId: null,
  solverId: null,
  protocol: "torrent",
  freeleech: false,
  createdAt: "2026-07-01T00:00:00Z",
  updatedAt: "2026-07-01T00:00:00Z",
}

function json(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } })
}

// Answers the _authenticated guard plus every GET the Indexers route fires on
// mount: the indexers list, definitions, per-slug status and capabilities.
function stubFetch() {
  vi.stubGlobal("fetch", vi.fn((request: Request) => {
    if (request.url.endsWith("/auth/me")) return Promise.resolve(json(ME))
    if (request.url.endsWith("/api/indexers")) return Promise.resolve(json([INDEXER]))
    if (request.url.endsWith("/api/definitions")) return Promise.resolve(json([]))
    if (request.url.endsWith("/status")) return Promise.resolve(json({ slug: "torrentleech", status: "healthy", events: [] }))
    if (request.url.endsWith("/capabilities")) return Promise.resolve(json({ categories: [] }))
    return Promise.resolve(json([]))
  }))
}

// A fixed-answer MediaQueryList stub — the route test only cares about the
// initial render, not live breakpoint changes (covered by useMediaQuery.test.tsx).
function stubMatchMedia(matches: boolean) {
  vi.stubGlobal("matchMedia", vi.fn().mockReturnValue({
    matches,
    media: "",
    addEventListener: () => {},
    removeEventListener: () => {},
    addListener: () => {},
    removeListener: () => {},
  }))
}

function renderIndexers() {
  const router = createRouter({ routeTree, history: createMemoryHistory({ initialEntries: ["/indexers"] }) })
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={queryClient}>
      <ThemeProvider>
        <RouterProvider router={router} />
      </ThemeProvider>
    </QueryClientProvider>
  )
}

describe("Indexers route — responsive layout (autobrr/harbrr#94)", () => {
  afterEach(() => vi.unstubAllGlobals())

  it("renders the cards layout under the mobile breakpoint", async () => {
    stubFetch()
    stubMatchMedia(true)
    renderIndexers()

    await waitFor(() => expect(screen.getByText("TorrentLeech")).toBeTruthy())
    expect(document.querySelector("[data-slug='torrentleech']")).toBeTruthy()
    expect(document.querySelector("table")).toBeNull()
  })

  it("renders the table layout at/above the md breakpoint", async () => {
    stubFetch()
    stubMatchMedia(false)
    renderIndexers()

    await waitFor(() => expect(screen.getByText("TorrentLeech")).toBeTruthy())
    expect(document.querySelector("table")).toBeTruthy()
  })
})
