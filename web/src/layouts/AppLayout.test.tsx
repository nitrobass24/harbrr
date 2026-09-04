import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { fireEvent, render, screen, waitFor } from "@testing-library/react"
import { createMemoryHistory, createRouter, RouterProvider } from "@tanstack/react-router"
import { describe, expect, it } from "vitest"
import { ThemeProvider } from "@/components/themes/theme-provider"
import { stubApi } from "@/test/stubApi"
import { routeTree } from "@/routeTree.gen"

const ME = { username: "admin", authMethod: "password", csrfToken: "tok" }

// me answers with the fixture; the queries the routed pages fire (dashboard tiles,
// indexer lists) get inert empty bodies.
function stubShell() {
  stubApi({
    "GET /api/auth/me": ME,
    "GET /api/indexers": [],
    "GET /api/indexers/stats": [],
    "GET /api/cache/stats": [],
    "GET /api/app-connections": [],
  })
}

function renderApp(initialEntries: string[] = ["/"]) {
  stubShell()

  const router = createRouter({
    routeTree,
    history: createMemoryHistory({ initialEntries }),
  })
  render(
    <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      <ThemeProvider>
        <RouterProvider router={router} />
      </ThemeProvider>
    </QueryClientProvider>
  )
  return router
}

describe("AppLayout shell", () => {
  it("renders the sidebar nav per the mockup for a signed-in user", async () => {
    stubShell()

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
    // Logout button and theme control in the sidebar footer.
    expect(screen.getByLabelText("Log out")).toBeTruthy()
    expect(screen.getByLabelText("Dark theme")).toBeTruthy()
  })
})

describe("responsive shell", () => {
  it("hides the sidebar and shows the mobile footer nav on small viewports (CSS breakpoint classes)", async () => {
    renderApp()

    const sidebar = await screen.findByTestId("sidebar")
    expect(sidebar.className).toMatch(/\bhidden\b/)
    expect(sidebar.className).toMatch(/md:flex/)

    const footer = screen.getByTestId("mobile-footer-nav")
    expect(footer.className).toMatch(/md:hidden/)
  })

  it("lists the footer nav's primary destinations plus a More overflow menu", async () => {
    renderApp()

    const footer = await screen.findByTestId("mobile-footer-nav")
    for (const label of ["Dashboard", "Indexers", "Applications", "Search"]) {
      expect(screen.getAllByRole("link", { name: label }).length).toBeGreaterThanOrEqual(1)
    }
    expect(footer.querySelector("a[href='/indexers']")).toBeTruthy()

    fireEvent.click(screen.getByRole("button", { name: "More" }))
    for (const label of ["Settings", "Cache", "Proxies & Solvers"]) {
      await waitFor(() => expect(screen.getAllByRole("link", { name: label }).length).toBeGreaterThanOrEqual(1))
    }
  })

  it("navigates when a footer nav link is clicked", async () => {
    const router = renderApp()

    const footer = await screen.findByTestId("mobile-footer-nav")
    const indexersLink = footer.querySelector("a[href='/indexers']") as HTMLElement
    fireEvent.click(indexersLink)

    await waitFor(() => expect(router.state.location.pathname).toBe("/indexers"))
  })
})
