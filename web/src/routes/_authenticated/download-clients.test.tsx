import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { render, screen, waitFor, within } from "@testing-library/react"
import { createMemoryHistory, createRouter, RouterProvider } from "@tanstack/react-router"
import { afterEach, describe, expect, it, vi } from "vitest"
import { ThemeProvider } from "@/components/themes/theme-provider"
import { routeTree } from "@/routeTree.gen"

const ME = { username: "admin", authMethod: "password", csrfToken: "tok" }

// autobrr/harbrr#300 — download's reuse path only exists for kind "qui" (AppsSection
// only ever offers "Download client" for a qui App), so this is the only kind the
// route's pre-pick has to handle.
const QUI_APP = {
  id: 4, kind: "qui", name: "qui-app", baseUrl: "http://qui:7476", username: "",
  apiKey: "<redacted>", harbrrUrl: "http://harbrr:7478", enabled: true,
  references: { appConnections: 0, announce: 0, download: 0 },
  createdAt: "2026-07-01T00:00:00Z", updatedAt: "2026-07-01T00:00:00Z",
}

function json(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } })
}

function stubFetchWithApps(apps: unknown[]) {
  vi.stubGlobal("fetch", vi.fn((request: Request) => {
    if (request.url.endsWith("/auth/me")) return Promise.resolve(json(ME))
    if (request.url.endsWith("/api/apps") && request.method === "GET") return Promise.resolve(json(apps))
    return Promise.resolve(json([]))
  }))
}

function renderDownloadClientsAt(path: string) {
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

// The download-clients route mirrors applications.tsx's own "Use as…" deep-link
// mechanism (autobrr/harbrr#300): DownloadClientsSection lives on its own page, not
// on /applications, so its pre-pick has to arrive via this route's own search params.
describe("Download-clients route — 'Use as…' search param", () => {
  afterEach(() => vi.unstubAllGlobals())

  it("?appId opens the add dialog pre-picked to that qui app, then clears the search", async () => {
    stubFetchWithApps([QUI_APP])
    const { router } = renderDownloadClientsAt("/download-clients?appId=4")

    const dialog = await screen.findByRole("dialog")
    await waitFor(() => expect(within(dialog).getByLabelText<HTMLInputElement>("Name").value).toBe("qui-app"))
    expect(within(dialog).getByLabelText<HTMLSelectElement>("Kind").value).toBe("qui")

    await waitFor(() => expect(router.state.location.search).toEqual({}))
  })

  it("a non-numeric appId degrades to a no-op (dialog stays closed, never crashes)", async () => {
    stubFetchWithApps([QUI_APP])
    renderDownloadClientsAt("/download-clients?appId=notanumber")

    await screen.findByRole("heading", { name: "Download Clients" })
    expect(screen.queryByRole("dialog")).toBeNull()
  })
})
