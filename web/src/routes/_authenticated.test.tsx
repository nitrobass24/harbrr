import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { fireEvent, render, screen } from "@testing-library/react"
import { createMemoryHistory, createRouter, RouterProvider } from "@tanstack/react-router"
import { afterEach, describe, expect, it, vi } from "vitest"
import { ThemeProvider } from "@/components/themes/theme-provider"
import { routeTree } from "@/routeTree.gen"

function json(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } })
}

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
  afterEach(() => vi.unstubAllGlobals())

  it("shows a retry state (not a login redirect) when the me-probe fails with a non-401 error", async () => {
    // me is a transient 500 (server restart / network blip), not a 401. Everything
    // else answers empty so the shell would render if we got that far.
    let meOk = false
    vi.stubGlobal("fetch", vi.fn((url: unknown) => {
      const u = String(url)
      if (u.endsWith("/auth/me")) return Promise.resolve(meOk ? json(ME) : json({ code: "internal", error: "boom" }, 500))
      return Promise.resolve(json([]))
    }))

    renderAt("/")

    // The guard must offer a retry, and must NOT have redirected to /login (whose
    // submit button reads "Sign in"). Losing the page on a transient blip is the bug.
    expect(await screen.findByRole("button", { name: "Retry" })).toBeTruthy()
    expect(screen.queryByRole("button", { name: "Sign in" })).toBeNull()

    // Retry after the server recovers loads the app shell — assert the signed-in
    // user chip (unique to the shell; "harbrr" also renders on the login card) and
    // that we did not end up on the login screen.
    meOk = true
    fireEvent.click(screen.getByRole("button", { name: "Retry" }))
    expect(await screen.findByText("admin")).toBeTruthy()
    expect(screen.queryByRole("button", { name: "Sign in" })).toBeNull()
  })
})
