import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { fireEvent, render, screen } from "@testing-library/react"
import { afterEach, describe, expect, it, vi } from "vitest"
import type { ReactNode } from "react"
import type { AppConnection } from "@/lib/api"
import { ConnectionCard } from "./ConnectionCard"
import type { ConnectionActions } from "./ConnectionCard"

const CONN: AppConnection = {
  id: 10,
  name: "sonarr-main",
  kind: "sonarr",
  baseUrl: "http://sonarr:8989",
  harbrrUrl: "http://harbrr:7478",
  enabled: true,
  syncLevel: "full",
  indexScope: "all",
  freeleechMode: "honor",
  priority: 0,
  syncProfileId: null,
  createdAt: "2026-07-01T00:00:00Z",
  updatedAt: "2026-07-01T00:00:00Z",
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

function stubFetch(port: number) {
  vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse({ port })))
}

function actions(overrides: Partial<ConnectionActions> = {}): ConnectionActions {
  return {
    onToggle: vi.fn(),
    onTest: vi.fn(),
    onSync: vi.fn(),
    onEdit: vi.fn(),
    onDelete: vi.fn(),
    onStatus: vi.fn(),
    onSelectIndexers: vi.fn(),
    onFixPort: vi.fn(),
    ...overrides,
  }
}

describe("ConnectionCard stale-port indicator", () => {
  afterEach(() => vi.unstubAllGlobals())

  it("shows no stale-port badge when the stored port matches the live port", async () => {
    stubFetch(7478)
    render(wrap(<ConnectionCard conn={CONN} actions={actions()} />))

    // Let the server-info query settle before asserting its absence.
    await screen.findByText("sonarr-main")
    expect(await screen.findByText("full sync")).toBeTruthy()
    expect(screen.queryByText("port may be outdated")).toBeNull()
  })

  it("shows a stale-port badge when the stored port differs from the live port", async () => {
    stubFetch(9999)
    render(wrap(<ConnectionCard conn={CONN} actions={actions()} />))

    expect(await screen.findByText("port may be outdated")).toBeTruthy()
  })

  it("shows no stale-port badge for a reverse-proxied URL with no explicit port", async () => {
    // Mirrors defaultHarbrrUrl()'s window.location.origin prefill behind a
    // TLS-terminating reverse proxy: no explicit port, so it is never
    // comparable to harbrr's internal listen port.
    stubFetch(7478)
    render(wrap(
      <ConnectionCard conn={{ ...CONN, harbrrUrl: "https://harbrr.example.com" }} actions={actions()} />
    ))

    await screen.findByText("sonarr-main")
    expect(await screen.findByText("full sync")).toBeTruthy()
    expect(screen.queryByText("port may be outdated")).toBeNull()
  })

  it("clicking the fix action proposes the port-rewritten URL with scheme/host/path kept", async () => {
    stubFetch(9999)
    const onFixPort = vi.fn()
    const conn = { ...CONN, harbrrUrl: "https://harbrr.example.com:7478/base" }
    render(wrap(<ConnectionCard conn={conn} actions={actions({ onFixPort })} />))

    const fix = await screen.findByLabelText("Update sonarr-main's harbrr URL port to 9999")
    fireEvent.click(fix)

    // The card only proposes: the parent confirms before anything is written,
    // since an explicit differing port can be a deliberate mapping/proxy.
    expect(onFixPort).toHaveBeenCalledTimes(1)
    expect(onFixPort).toHaveBeenCalledWith(conn, "https://harbrr.example.com:9999/base")
  })
})
