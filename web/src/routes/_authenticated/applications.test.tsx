import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { fireEvent, render, screen, waitFor, within } from "@testing-library/react"
import { createMemoryHistory, createRouter, RouterProvider } from "@tanstack/react-router"
import { afterEach, describe, expect, it, vi } from "vitest"
import { ThemeProvider } from "@/components/themes/theme-provider"
import { routeTree } from "@/routeTree.gen"

const ME = { username: "admin", authMethod: "password", csrfToken: "tok" }

// The distinctive body a failed create returns; ApiClient.toError lifts `error`
// into APIError.message, which ConnectionForm renders. Unique on the page so its
// presence/absence is an unambiguous signal for the stale-error assertion.
const CREATE_ERROR = "app connection create exploded"

const SONARR_APP = {
  id: 7, kind: "sonarr", name: "sonarr-app", baseUrl: "http://sonarr:8989", username: "",
  apiKey: "<redacted>", harbrrUrl: "http://harbrr:7478", enabled: true,
  references: { appConnections: 0, announce: 0, download: 0 },
  createdAt: "2026-07-01T00:00:00Z", updatedAt: "2026-07-01T00:00:00Z",
}

const QUI_APP = {
  id: 8, kind: "qui", name: "qui-app", baseUrl: "http://qui:7476", username: "",
  apiKey: "<redacted>", harbrrUrl: "http://harbrr:7478", enabled: true,
  references: { appConnections: 0, announce: 0, download: 0 },
  createdAt: "2026-07-01T00:00:00Z", updatedAt: "2026-07-01T00:00:00Z",
}

function json(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } })
}

// stubFetch admits the _authenticated guard (me → 200) and answers every GET the
// Applications route + its sections fire on mount (app-connections, announce-
// connections, sync-profiles) with an empty list. The one mutation we exercise —
// POST /app-connections (useCreateConnection) — fails 500 so create.error is set.
function stubFetch() {
  vi.stubGlobal("fetch", vi.fn((request: Request) => {
    if (request.url.endsWith("/auth/me")) return Promise.resolve(json(ME))
    if (request.url.endsWith("/app-connections") && request.method === "POST") {
      return Promise.resolve(json({ error: CREATE_ERROR, code: "internal" }, 500))
    }
    // Every other GET (app-connections list, announce-connections, sync-profiles,
    // and any incidental probe) is fine as an empty list.
    return Promise.resolve(json([]))
  }))
}

function renderApplications() {
  const router = createRouter({ routeTree, history: createMemoryHistory({ initialEntries: ["/applications"] }) })
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={queryClient}>
      <ThemeProvider>
        <RouterProvider router={router} />
      </ThemeProvider>
    </QueryClientProvider>
  )
}

// Same shell as stubFetch, but answers GET /api/apps with a caller-supplied list
// instead of an empty one — needed for the "Use as…" pre-pick, which resolves the
// deep-linked appId against the apps list.
function stubFetchWithApps(apps: unknown[]) {
  vi.stubGlobal("fetch", vi.fn((request: Request) => {
    if (request.url.endsWith("/auth/me")) return Promise.resolve(json(ME))
    if (request.url.endsWith("/api/apps") && request.method === "GET") return Promise.resolve(json(apps))
    return Promise.resolve(json([]))
  }))
}

// Renders at an arbitrary path (for exercising search-param handling) and returns the
// router alongside the render result, so a test can inspect the location after a
// navigate({ search: {}, replace: true }) clear.
function renderApplicationsAt(path: string) {
  const router = createRouter({ routeTree, history: createMemoryHistory({ initialEntries: [path] }) })
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const result = render(
    <QueryClientProvider client={queryClient}>
      <ThemeProvider>
        <RouterProvider router={router} />
      </ThemeProvider>
    </QueryClientProvider>
  )
  return { ...result, router }
}

describe("Applications route — stale create error", () => {
  afterEach(() => vi.unstubAllGlobals())

  // Gates the U16-F5 fix: applications.tsx's ConnectionDialog onClose must call
  // create.reset()/update.reset(). Without those, a failed create leaves the
  // mutation error object live, so reopening the Add dialog (fields remount, but
  // the mutation error does not) re-shows the previous error. This exercises the
  // REAL route so the container's onClose wiring is what's under test.
  it("does not resurface a failed-create error when the Add dialog is reopened", async () => {
    stubFetch()
    renderApplications()

    // Shell + page have rendered once the Applications heading is present.
    await screen.findByRole("heading", { name: "Applications" })

    // Open the Add dialog (header + empty-state both offer "Add application"; the
    // header one is first). Fill the required create fields (kind defaults to
    // sonarr; harbrr URL is prefilled) and submit.
    fireEvent.click(screen.getAllByRole("button", { name: "Add application" })[0])
    let dialog = await screen.findByRole("dialog")
    fireEvent.change(within(dialog).getByLabelText("Name"), { target: { value: "sonarr-new" } })
    fireEvent.change(within(dialog).getByLabelText("App base URL"), { target: { value: "http://sonarr:8989" } })
    fireEvent.change(within(dialog).getByLabelText("App API key"), { target: { value: "app-key" } })
    fireEvent.click(within(dialog).getByRole("button", { name: "Add application" }))

    // The failed create surfaces its message inside the dialog.
    expect(await within(dialog).findByText(CREATE_ERROR)).toBeTruthy()

    // Close the dialog via the built-in X (accessible name "Close") — this fires
    // Dialog onOpenChange(false) → onClose, the exact path the fix instruments.
    fireEvent.click(within(dialog).getByRole("button", { name: "Close" }))
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull())

    // Reopen the Add dialog. With the reset() calls the error is gone; without
    // them the stale create.error re-renders and this assertion fails.
    fireEvent.click(screen.getAllByRole("button", { name: "Add application" })[0])
    dialog = await screen.findByRole("dialog")
    expect(within(dialog).queryByText(CREATE_ERROR)).toBeNull()
  })
})

// autobrr/harbrr#300 — AppsSection's "Use as…" deep-links here via search params.
describe("Applications route — 'Use as…' search params", () => {
  afterEach(() => vi.unstubAllGlobals())

  it("?create=sync&appId opens the sync dialog pre-picked, then clears the search", async () => {
    stubFetchWithApps([SONARR_APP])
    const { router } = renderApplicationsAt("/applications?create=sync&appId=7")

    const dialog = await screen.findByRole("dialog")
    // The pick applies once the app-list fetch resolves, a tick after the dialog itself
    // mounts — wait for the field to actually carry the pre-picked value.
    await waitFor(() => expect(within(dialog).getByLabelText<HTMLInputElement>("Name").value).toBe("sonarr-app"))
    expect(within(dialog).getByLabelText<HTMLSelectElement>("Kind").value).toBe("sonarr")
    expect(within(dialog).getByLabelText<HTMLSelectElement>("App").value).toBe("7")

    // The pick reuses the app: the "New app…" fields don't show.
    expect(within(dialog).queryByLabelText("App base URL")).toBeNull()

    await waitFor(() => expect(router.state.location.search).toEqual({}))
  })

  it("?create=announce&appId opens the announce dialog pre-picked, then clears the search", async () => {
    stubFetchWithApps([QUI_APP])
    const { router } = renderApplicationsAt("/applications?create=announce&appId=8")

    const dialog = await screen.findByRole("dialog")
    await waitFor(() => expect(within(dialog).getByLabelText<HTMLInputElement>("Name").value).toBe("qui-app"))
    expect(within(dialog).getByLabelText<HTMLSelectElement>("Kind").value).toBe("qui")

    await waitFor(() => expect(router.state.location.search).toEqual({}))
  })

  it("an unrecognized `create` value degrades to a no-op (never opens a dialog, never crashes)", async () => {
    stubFetchWithApps([SONARR_APP])
    renderApplicationsAt("/applications?create=bogus&appId=7")

    await screen.findByRole("heading", { name: "Applications" })
    expect(screen.queryByRole("dialog")).toBeNull()
  })

  it("a non-numeric appId degrades to a no-op (dialog stays closed; the stray search still clears)", async () => {
    stubFetchWithApps([SONARR_APP])
    const { router } = renderApplicationsAt("/applications?create=sync&appId=notanumber")

    await screen.findByRole("heading", { name: "Applications" })
    expect(screen.queryByRole("dialog")).toBeNull()
    await waitFor(() => expect(router.state.location.search).toEqual({}))
  })
})
