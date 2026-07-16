import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { fireEvent, render, screen, waitFor } from "@testing-library/react"
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

function renderApp(initialEntries: string[] = ["/"]) {
  vi.stubGlobal("fetch", vi.fn().mockImplementation((url: unknown) => {
    if (String(url).endsWith("/auth/me")) return Promise.resolve(meResponse())
    return Promise.resolve(new Response("[]", { status: 200, headers: { "Content-Type": "application/json" } }))
  }))

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
    // Logout button and theme control in the sidebar footer.
    expect(screen.getByLabelText("Log out")).toBeTruthy()
    expect(screen.getByLabelText("Dark theme")).toBeTruthy()
  })
})

describe("responsive shell", () => {
  afterEach(() => vi.unstubAllGlobals())

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
