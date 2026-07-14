import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { fireEvent, render, screen } from "@testing-library/react"
import { createMemoryHistory, createRouter, RouterProvider } from "@tanstack/react-router"
import { afterEach, describe, expect, it, vi } from "vitest"
import { ThemeProvider } from "@/components/themes/theme-provider"
import { routeTree } from "@/routeTree.gen"

// json builds a stub Response with a JSON body.
function json(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } })
}

// stubAuthFetch answers the bootstrap probes for a first-run (no session) visitor:
// /auth/me is 401 (unauthenticated), GET /auth/setup reports NOT complete, and
// POST /auth/setup (creating the admin) succeeds. Crucially GET /auth/setup keeps
// reporting {setupComplete:false} — so the only way the /login guard can learn setup
// is done is the cache the setup mutation seeds on success (the U16-F1 fix).
function stubAuthFetch() {
  vi.stubGlobal("fetch", vi.fn((request: Request) => {
    if (request.url.endsWith("/auth/me")) return Promise.resolve(json({ code: "unauthorized", error: "no session" }, 401))
    if (request.url.endsWith("/auth/setup") && request.method === "POST") return Promise.resolve(json({ username: "admin" }, 201))
    if (request.url.endsWith("/auth/setup")) return Promise.resolve(json({ setupComplete: false }))
    return Promise.resolve(json({}))
  }))
}

function renderAt(path: string) {
  const router = createRouter({ routeTree, history: createMemoryHistory({ initialEntries: [path] }) })
  // staleTime:Infinity mirrors the production 5s window closely enough to reproduce
  // the worst case: the setup-status query is never refetched during the redirect, so
  // the /login guard must rely on the seeded cache — exactly where the bug bit.
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Infinity } } })
  return render(
    <QueryClientProvider client={queryClient}>
      <ThemeProvider>
        <RouterProvider router={router} />
      </ThemeProvider>
    </QueryClientProvider>
  )
}

describe("Setup route", () => {
  afterEach(() => vi.unstubAllGlobals())

  it("lands on the login screen after creating the admin, without bouncing back to setup", async () => {
    stubAuthFetch()
    renderAt("/setup")

    // The first-run form renders while the bootstrap probes settle.
    fireEvent.change(await screen.findByLabelText("Username"), { target: { value: "admin" } })
    fireEvent.change(screen.getByLabelText("Password"), { target: { value: "password123" } })
    fireEvent.change(screen.getByLabelText("Confirm password"), { target: { value: "password123" } })
    fireEvent.click(screen.getByRole("button", { name: "Create account" }))

    // After success the app must be on /login (its "Sign in" submit button). Without
    // the fix the /login guard reads the stale {setupComplete:false} cache and
    // redirects straight back to /setup, whose "Create the admin account" title would
    // then be showing instead.
    expect(await screen.findByRole("button", { name: "Sign in" })).toBeTruthy()
    expect(screen.queryByText("Create the admin account")).toBeNull()
  })

  it("shows a retry state (not the create-admin form) when the setup probe errors", async () => {
    // me is unauthenticated, but the setup-status probe fails (500). The form must
    // NOT render — submitting it on a configured instance would just 409. Show a
    // retry instead; on recovery the real first-run form appears.
    let setupOk = false
    vi.stubGlobal("fetch", vi.fn((request: Request) => {
      if (request.url.endsWith("/auth/me")) return Promise.resolve(json({ code: "unauthorized" }, 401))
      if (request.url.endsWith("/auth/setup")) {
        return Promise.resolve(setupOk ? json({ setupComplete: false }) : json({ code: "internal" }, 500))
      }
      return Promise.resolve(json({}))
    }))

    renderAt("/setup")

    expect(await screen.findByRole("button", { name: "Retry" })).toBeTruthy()
    expect(screen.queryByLabelText("Username")).toBeNull()

    // Retry after the probe recovers reveals the create-admin form.
    setupOk = true
    fireEvent.click(screen.getByRole("button", { name: "Retry" }))
    expect(await screen.findByText("Create the admin account")).toBeTruthy()
    expect(screen.getByLabelText("Username")).toBeTruthy()
  })
})
