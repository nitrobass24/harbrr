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

function json(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } })
}

// stubFetch admits the _authenticated guard (me → 200) and answers every GET the
// Applications route + its sections fire on mount (app-connections, announce-
// connections, sync-profiles) with an empty list. The one mutation we exercise —
// POST /app-connections (useCreateConnection) — fails 500 so create.error is set.
function stubFetch() {
  vi.stubGlobal("fetch", vi.fn((url: unknown, init?: RequestInit) => {
    const u = String(url)
    const method = (init?.method ?? "GET").toUpperCase()
    if (u.endsWith("/auth/me")) return Promise.resolve(json(ME))
    if (u.endsWith("/app-connections") && method === "POST") {
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
