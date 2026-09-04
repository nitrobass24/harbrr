import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { fireEvent, render, screen } from "@testing-library/react"
import { createMemoryHistory, createRouter, RouterProvider } from "@tanstack/react-router"
import { describe, expect, it } from "vitest"
import { ThemeProvider } from "@/components/themes/theme-provider"
import { stubApi } from "@/test/stubApi"
import { routeTree } from "@/routeTree.gen"

const ME = { username: "admin", authMethod: "password", csrfToken: "tok" }

function renderAt(path: string) {
  const router = createRouter({ routeTree, history: createMemoryHistory({ initialEntries: [path] }) })
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Infinity } } })
  return render(
    <QueryClientProvider client={queryClient}>
      <ThemeProvider>
        <RouterProvider router={router} />
      </ThemeProvider>
    </QueryClientProvider>
  )
}

describe("_authenticated guard", () => {
  it("shows a retry state (not a login redirect) when the me-probe fails with a non-401 error", async () => {
    // me is a transient 500 (server restart / network blip), not a 401. Everything
    // else answers empty so the shell would render if we got that far.
    let meOk = false
    stubApi({
      "GET /api/auth/me": () =>
        meOk ? Response.json(ME) : Response.json({ code: "internal", error: "boom" }, { status: 500 }),
      "GET /api/indexers": [],
      "GET /api/indexers/stats": [],
      "GET /api/cache/stats": [],
      "GET /api/app-connections": [],
    })

    renderAt("/")

    // The guard must offer a retry, and must NOT have redirected to /login (whose
    // submit button reads "Sign in"). Losing the page on a transient blip is the bug.
    expect(await screen.findByRole("button", { name: "Retry" })).toBeTruthy()
    expect(screen.queryByRole("button", { name: "Sign in" })).toBeNull()

    // Retry after the server recovers loads the app shell — assert the logout button
    // (unique to the authenticated shell) and that we did not end up on the login screen.
    meOk = true
    fireEvent.click(screen.getByRole("button", { name: "Retry" }))
    expect(await screen.findByLabelText("Log out")).toBeTruthy()
    expect(screen.queryByRole("button", { name: "Sign in" })).toBeNull()
  })
})
