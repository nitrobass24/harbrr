import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { fireEvent, render, screen, waitFor } from "@testing-library/react"
import { createMemoryHistory, createRouter, RouterProvider } from "@tanstack/react-router"
import { afterEach, describe, expect, it, vi } from "vitest"
import { ThemeProvider } from "@/components/themes/theme-provider"
import { routeTree } from "@/routeTree.gen"

function json(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } })
}

const ME = { username: "admin", authMethod: "password", csrfToken: "tok" }

// stubAuthFetch answers a logged-out visitor: /auth/me is 401 until a successful
// POST /auth/login flips the session on, setup is already complete (so /login does
// not bounce to /setup), and every other read answers with an empty list so the
// authenticated shell renders.
function stubAuthFetch() {
  let loggedIn = false
  vi.stubGlobal("fetch", vi.fn((request: Request) => {
    if (request.url.endsWith("/auth/login") && request.method === "POST") {
      loggedIn = true
      return Promise.resolve(json({}))
    }
    if (request.url.endsWith("/auth/me")) {
      return Promise.resolve(loggedIn ? json(ME) : json({ code: "unauthorized", error: "no session" }, 401))
    }
    if (request.url.endsWith("/auth/setup")) return Promise.resolve(json({ setupComplete: true }))
    return Promise.resolve(json([]))
  }))
}

function renderAt(path: string) {
  const router = createRouter({ routeTree, history: createMemoryHistory({ initialEntries: [path] }) })
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Infinity } } })
  render(
    <QueryClientProvider client={queryClient}>
      <ThemeProvider>
        <RouterProvider router={router} />
      </ThemeProvider>
    </QueryClientProvider>
  )
  return router
}

async function signIn() {
  fireEvent.change(await screen.findByLabelText("Username"), { target: { value: "admin" } })
  fireEvent.change(screen.getByLabelText("Password"), { target: { value: "password123" } })
  fireEvent.click(screen.getByRole("button", { name: "Sign in" }))
}

describe("Login redirect", () => {
  afterEach(() => vi.unstubAllGlobals())

  it("returns the user to ?redirect after a successful sign-in", async () => {
    stubAuthFetch()
    const router = renderAt("/login?redirect=%2Findexers")

    await signIn()

    await waitFor(() => expect(router.state.location.pathname).toBe("/indexers"))
  })

  it("neutralises an off-site ?redirect (open-redirect blocked) to /", async () => {
    stubAuthFetch()
    // A protocol-relative //evil.com would navigate to another origin if trusted.
    const router = renderAt("/login?redirect=%2F%2Fevil.com")

    await signIn()

    await waitFor(() => expect(router.state.location.pathname).toBe("/"))
    // Never left the app's own origin.
    expect(router.state.location.pathname.startsWith("//")).toBe(false)
  })

  it("bounces a logged-out deep-link to /login carrying the attempted path", async () => {
    stubAuthFetch()
    const router = renderAt("/indexers")

    // The guard sees no session and routes to /login with redirect=/indexers so a
    // later sign-in can return there. (No sign-in here — we assert the bounce.)
    await waitFor(() => expect(router.state.location.pathname).toBe("/login"))
    expect(router.state.location.search.redirect).toBe("/indexers")
    expect(await screen.findByRole("button", { name: "Sign in" })).toBeTruthy()
  })
})
