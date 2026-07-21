import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { render, screen } from "@testing-library/react"
import { afterEach, describe, expect, it, vi } from "vitest"
import type { ReactNode } from "react"
import { withMemoryRouter } from "@/test/router"
import type { App } from "@/lib/api"
import { ManagedByAppHint } from "./ManagedByAppHint"

const QUI_APP: App = {
  id: 9, kind: "qui", name: "qui-main-app", baseUrl: "http://qui:7476", username: "",
  apiKey: "<redacted>", harbrrUrl: "", enabled: true,
  references: { appConnections: 0, announce: 0, download: 0 },
  createdAt: "2026-07-01T00:00:00Z", updatedAt: "2026-07-01T00:00:00Z",
}

function jsonResponse(body: unknown): Response {
  return new Response(JSON.stringify(body), { status: 200, headers: { "Content-Type": "application/json" } })
}

function wrap(children: ReactNode) {
  return (
    <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      {children}
    </QueryClientProvider>
  )
}

// ManagedByAppHint links via the router `Link`, which needs router context — wrap
// with the shared memory-router harness instead of the full app routeTree.
function renderWithRouter(ui: ReactNode) {
  return render(wrap(withMemoryRouter(ui)))
}

describe("ManagedByAppHint", () => {
  afterEach(() => vi.unstubAllGlobals())

  it("renders nothing without an appId", () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse([QUI_APP])))
    renderWithRouter(<ManagedByAppHint appId={undefined} />)

    expect(screen.queryByText(/managed by app/)).toBeNull()
  })

  it("renders the app's name as a link to Settings' apps section", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse([QUI_APP])))
    renderWithRouter(<ManagedByAppHint appId={QUI_APP.id} />)

    const link = await screen.findByRole("link", { name: "qui-main-app" })
    expect(link.getAttribute("href")).toBe("/settings#apps-section")
  })
})
