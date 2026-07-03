import { render, screen } from "@testing-library/react"
import { createMemoryHistory, createRouter, RouterProvider } from "@tanstack/react-router"
import { describe, expect, it } from "vitest"
import { ThemeProvider } from "@/components/themes/theme-provider"
import { routeTree } from "@/routeTree.gen"

describe("AppLayout shell", () => {
  it("renders the sidebar nav per the mockup", async () => {
    const router = createRouter({
      routeTree,
      history: createMemoryHistory({ initialEntries: ["/"] }),
    })
    render(
      <ThemeProvider>
        <RouterProvider router={router} />
      </ThemeProvider>
    )

    // Logo + every nav destination from docs/webui-scope.md §3. Dashboard also
    // appears as the active page heading, so assert links by role + name.
    expect(await screen.findByText("harbrr")).toBeTruthy()
    for (const label of ["Dashboard", "Indexers", "Search", "Applications", "Settings"]) {
      expect(screen.getByRole("link", { name: label })).toBeTruthy()
    }
    // Group titles.
    expect(screen.getByText("Manage")).toBeTruthy()
    expect(screen.getByText("Sync")).toBeTruthy()
    // Theme control is present in the footer.
    expect(screen.getByLabelText("Dark theme")).toBeTruthy()
  })
})
