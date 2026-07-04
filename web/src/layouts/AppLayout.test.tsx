import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { render, screen } from "@testing-library/react"
import { createMemoryHistory, createRouter, RouterProvider } from "@tanstack/react-router"
import { afterEach, describe, expect, it, vi } from "vitest"
import { ThemeProvider } from "@/components/themes/theme-provider"
import { routeTree } from "@/routeTree.gen"

const ME = { username: "admin", authMethod: "password", csrfToken: "tok" }

function meResponse(): Response {
  return new Response(JSON.stringify(ME), {
    status: 200,
    headers: { "Content-Type": "application/json" },
  })
}

describe("AppLayout shell", () => {
  afterEach(() => vi.unstubAllGlobals())

  it("renders the sidebar nav per the mockup for a signed-in user", async () => {
    // me answers with the fixture; every other query (dashboard lists) gets [].
    vi.stubGlobal("fetch", vi.fn().mockImplementation((url: unknown) => {
      if (String(url).endsWith("/auth/me")) return Promise.resolve(meResponse())
      return Promise.resolve(new Response("[]", { status: 200, headers: { "Content-Type": "application/json" } }))
    }))

    const router = createRouter({
      routeTree,
      history: createMemoryHistory({ initialEntries: ["/"] }),
    })
    render(
      <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
        <ThemeProvider>
          <RouterProvider router={router} />
        </ThemeProvider>
      </QueryClientProvider>
    )

    // Logo + every nav destination from docs/webui-scope.md §3. Labels also
    // appear on the page rendered at "/" (Dashboard heading, quick links), so
    // assert at-least-one link per destination.
    expect(await screen.findByText("harbrr")).toBeTruthy()
    for (const label of ["Dashboard", "Indexers", "Search", "Applications", "Settings"]) {
      expect(screen.getAllByRole("link", { name: label }).length).toBeGreaterThanOrEqual(1)
    }
    // Group titles.
    expect(screen.getByText("Manage")).toBeTruthy()
    expect(screen.getByText("Sync")).toBeTruthy()
    // Signed-in chip with logout, and the theme control.
    expect(await screen.findByText("admin")).toBeTruthy()
    expect(screen.getByLabelText("Log out")).toBeTruthy()
    expect(screen.getByLabelText("Dark theme")).toBeTruthy()
  })
})
