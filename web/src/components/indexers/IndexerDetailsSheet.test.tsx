import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { render, screen } from "@testing-library/react"
import { afterEach, describe, expect, it, vi } from "vitest"
import { IndexerDetailsSheet } from "./IndexerDetailsSheet"

function json(body: unknown): Response {
  return new Response(JSON.stringify(body), { status: 200, headers: { "Content-Type": "application/json" } })
}

describe("IndexerDetailsSheet", () => {
  afterEach(() => vi.unstubAllGlobals())

  it("renders the summed failure count from the per-kind object without crashing", async () => {
    vi.stubGlobal("fetch", vi.fn().mockImplementation((request: Request) => {
      const path = request.url
      if (path.includes("/stats")) {
        return Promise.resolve(json({
          slug: "torrentleech",
          queries: 10,
          grabs: 2,
          avgResponseMs: 120,
          failures: { authFailure: 1, rateLimited: 2, parseError: 0, antiBot: 3 },
        }))
      }
      if (path.includes("/status")) {
        return Promise.resolve(json({ slug: "torrentleech", status: "healthy", events: [] }))
      }
      if (path.includes("/capabilities")) {
        return Promise.resolve(json({ modes: { search: ["q"] } }))
      }
      if (path.includes("/diagnostics")) {
        return Promise.resolve(json({ slug: "torrentleech", captures: [] }))
      }
      return Promise.resolve(json({}))
    }))

    render(
      <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
        <IndexerDetailsSheet slug="torrentleech" onClose={vi.fn()} />
      </QueryClientProvider>
    )

    // 1 + 2 + 0 + 3 = 6. If the object were rendered directly instead of the
    // sum, React would throw "Objects are not valid as a React child".
    expect(await screen.findByText("6")).toBeTruthy()
    // No grab attempts recorded: the bare count, never a "0%" success rate.
    expect(await screen.findByText("2")).toBeTruthy()
    expect(screen.queryByText("By category")).toBeNull()
  })

  it("renders the grab success rate and the per-category tallies", async () => {
    vi.stubGlobal("fetch", vi.fn().mockImplementation((request: Request) => {
      const path = request.url
      if (path.includes("/stats")) {
        return Promise.resolve(json({
          slug: "torrentleech",
          queries: 10,
          grabAttempts: 6,
          grabs: 5,
          grabSuccessRate: 5 / 6,
          avgResponseMs: 120,
          failures: { authFailure: 0, rateLimited: 0, parseError: 0, antiBot: 0 },
          categories: [
            { id: 2000, name: "Movies", results: 40, grabs: 4 },
            { id: 0, name: "Uncategorized", results: 2, grabs: 1 },
          ],
        }))
      }
      if (path.includes("/status")) {
        return Promise.resolve(json({ slug: "torrentleech", status: "healthy", events: [] }))
      }
      if (path.includes("/capabilities")) {
        return Promise.resolve(json({ modes: { search: ["q"] } }))
      }
      if (path.includes("/diagnostics")) {
        return Promise.resolve(json({ slug: "torrentleech", captures: [] }))
      }
      return Promise.resolve(json({}))
    }))

    render(
      <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
        <IndexerDetailsSheet slug="torrentleech" onClose={vi.fn()} />
      </QueryClientProvider>
    )

    expect(await screen.findByText("5 · 1 failed (83%)")).toBeTruthy()
    expect(await screen.findByText("Movies")).toBeTruthy()
    expect(await screen.findByText("Uncategorized")).toBeTruthy()
    expect(await screen.findByText("40")).toBeTruthy()
  })

  // #389 PR 3: a failing indexer has to say what broke, since when, and when harbrr
  // tries again — in operator language, never the raw kind constant.
  it("renders the failing reason, the streak start and the next attempt", async () => {
    const hourAgo = new Date(Date.now() - 60 * 60_000).toISOString()
    stubStatus({
      slug: "torrentleech",
      status: "failing",
      events: [{ kind: "auth_failure", detail: "401 from login", occurred_at: hourAgo }],
      failingSince: new Date(Date.now() - 3 * 60 * 60_000).toISOString(),
      disabledTill: new Date(Date.now() + 30 * 60_000).toISOString(),
    })

    renderSheet()

    expect(await screen.findByText("Failing")).toBeTruthy()
    expect(screen.getAllByText("login failed").length).toBeGreaterThan(0)
    expect(screen.queryByText("auth_failure")).toBeNull()
    expect(screen.getByText("Failing since")).toBeTruthy()
    expect(screen.getByText("3h ago")).toBeTruthy()
    expect(screen.getByText("Next attempt")).toBeTruthy()
  })

  // "unknown" is honest absence, not an error: it must read as never-tested and must
  // not offer a reason, a streak start, or a retry time.
  it("renders the never-tested copy for unknown", async () => {
    stubStatus({ slug: "torrentleech", status: "unknown", events: [] })

    renderSheet()

    expect(await screen.findByText("Never tested")).toBeTruthy()
    expect(screen.getByText(/Nothing has queried or tested it yet/)).toBeTruthy()
    expect(screen.queryByText("Failing since")).toBeNull()
    expect(screen.queryByText("Next attempt")).toBeNull()
  })

  // #390 PR 2: the diagnostics section has to separate the two failures operators
  // conflate — nothing matched the rows selector vs a row matched but a field did
  // not — and expand to the redacted evidence harbrr already held.
  it("renders both capture kinds with their selector and definition path", async () => {
    const at = new Date(Date.now() - 5 * 60_000).toISOString()
    stubStatus({ slug: "torrentleech", status: "failing", events: [] }, [
      {
        kind: "parse_error", occurred_at: at, method: "GET", status: 200,
        url: "https://cap.invalid/api/REDACTED?passkey=REDACTED",
        headers: { "Content-Type": "text/html", "Set-Cookie": "REDACTED" },
        body: "<html>drifted</html>",
        selectorMiss: { kind: "no_rows", selector: "tr.torrent", path: "/search/rows" },
      },
      {
        kind: "parse_error", occurred_at: at, method: "GET", status: 200,
        url: "https://cap.invalid/browse",
        selectorMiss: { kind: "fields", selector: "a.title", path: "/search/fields/title" },
      },
    ])

    renderSheet()

    expect(await screen.findByText(/No rows matched — tr\.torrent at \/search\/rows/)).toBeTruthy()
    expect(screen.getByText(/Rows matched, field missed — a\.title at \/search\/fields\/title/)).toBeTruthy()
    // The redacted evidence is present, and the raw kind constant never is.
    expect(screen.getByText("GET https://cap.invalid/api/REDACTED?passkey=REDACTED")).toBeTruthy()
    expect(screen.getByText("<html>drifted</html>")).toBeTruthy()
    expect(screen.queryByText("parse_error")).toBeNull()
    expect(screen.queryByText("No captured failures.")).toBeNull()
  })

  // Nothing captured is the normal state and must read as such, not as an error.
  it("says so when nothing has been captured", async () => {
    stubStatus({ slug: "torrentleech", status: "healthy", events: [] })

    renderSheet()

    expect(await screen.findByText("No captured failures.")).toBeTruthy()
  })

  // Healthy stays quiet: the dot is the whole story, no detail rows.
  it("adds no detail rows for a healthy indexer", async () => {
    stubStatus({ slug: "torrentleech", status: "healthy", events: [] })

    renderSheet()

    expect(await screen.findByText("Healthy")).toBeTruthy()
    expect(screen.queryByText("Why")).toBeNull()
    expect(screen.queryByText("Failing since")).toBeNull()
  })
})

// stubStatus serves the given /status body and empty stats/capabilities, so a health
// test only states the health fixture it cares about. captures (optional) is the
// /diagnostics body's ring.
function stubStatus(status: unknown, captures: unknown[] = []) {
  vi.stubGlobal("fetch", vi.fn().mockImplementation((request: Request) => {
    if (request.url.includes("/status")) return Promise.resolve(json(status))
    if (request.url.includes("/stats")) {
      return Promise.resolve(json({ slug: "torrentleech", queries: 0, grabs: 0 }))
    }
    if (request.url.includes("/capabilities")) return Promise.resolve(json({ modes: { search: ["q"] } }))
    if (request.url.includes("/diagnostics")) return Promise.resolve(json({ slug: "torrentleech", captures }))
    return Promise.resolve(json({}))
  }))
}

function renderSheet() {
  render(
    <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      <IndexerDetailsSheet slug="torrentleech" onClose={vi.fn()} />
    </QueryClientProvider>
  )
}
