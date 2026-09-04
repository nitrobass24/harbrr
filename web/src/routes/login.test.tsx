import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { fireEvent, render, screen, waitFor } from "@testing-library/react"
import { createMemoryHistory, createRouter, RouterProvider } from "@tanstack/react-router"
import { describe, expect, it } from "vitest"
import { ThemeProvider } from "@/components/themes/theme-provider"
import { stubApi } from "@/test/stubApi"
import { routeTree } from "@/routeTree.gen"

const ME = { username: "admin", authMethod: "password", csrfToken: "tok" }

const OIDC_DISABLED = { enabled: false, authorizationUrl: "", disableBuiltInLogin: false, issuerUrl: "" }

// stubAuthFetch answers a logged-out visitor: /auth/me is 401 until a successful
// POST /auth/login flips the session on, setup is already complete (so /login does
// not bounce to /setup), OIDC is disabled unless oidc overrides it, and the reads
// the post-login pages fire answer with an empty list so the shell renders.
function stubAuthFetch(oidc: typeof OIDC_DISABLED = OIDC_DISABLED) {
  let loggedIn = false
  stubApi({
    "POST /api/auth/login": () => {
      loggedIn = true
      return Response.json({})
    },
    "GET /api/auth/me": () =>
      loggedIn ? Response.json(ME) : Response.json({ code: "unauthorized", error: "no session" }, { status: 401 }),
    "GET /api/auth/setup": { setupComplete: true },
    "GET /api/auth/oidc/config": oidc,
    "GET /api/indexers": [],
    "GET /api/indexers/stats": [],
    "GET /api/definitions": [],
    "GET /api/cache/stats": [],
    "GET /api/app-connections": [],
  })
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

describe("Login OIDC/SSO (autobrr/harbrr#9)", () => {
  it("shows both the password form and the SSO button when OIDC coexists with built-in login", async () => {
    stubAuthFetch({ enabled: true, authorizationUrl: "https://idp.example.com/authorize?state=x", disableBuiltInLogin: false, issuerUrl: "https://idp.example.com" })
    renderAt("/login")

    expect(await screen.findByLabelText("Username")).toBeTruthy()
    expect(await screen.findByRole("button", { name: "Sign in with SSO" })).toBeTruthy()
  })

  it("hides the password form when disableBuiltInLogin is set", async () => {
    stubAuthFetch({ enabled: true, authorizationUrl: "https://idp.example.com/authorize?state=x", disableBuiltInLogin: true, issuerUrl: "https://idp.example.com" })
    renderAt("/login")

    expect(await screen.findByRole("button", { name: "Sign in with SSO" })).toBeTruthy()
    expect(screen.queryByLabelText("Username")).toBeNull()
  })

  it("navigates the full page to the authorization URL on SSO click", async () => {
    stubAuthFetch({ enabled: true, authorizationUrl: "https://idp.example.com/authorize?state=x", disableBuiltInLogin: false, issuerUrl: "https://idp.example.com" })
    renderAt("/login")

    const button = await screen.findByRole("button", { name: "Sign in with SSO" })
    // jsdom doesn't implement navigation; assert intent via the click handler
    // rather than window.location, which jsdom throws on when reassigned.
    let navigatedTo = ""
    const original = window.location
    Object.defineProperty(window, "location", { value: { ...original, set href(v: string) { navigatedTo = v } }, writable: true })
    fireEvent.click(button)
    expect(navigatedTo).toBe("https://idp.example.com/authorize?state=x")
    Object.defineProperty(window, "location", { value: original, writable: true })
  })
})
