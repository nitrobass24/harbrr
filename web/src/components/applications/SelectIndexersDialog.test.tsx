import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { fireEvent, render, screen, waitFor } from "@testing-library/react"
import { afterEach, describe, expect, it, vi } from "vitest"
import type { ReactNode } from "react"
import type { AppConnection, ConnectionStatus, Instance } from "@/lib/api"
import { SelectIndexersDialog } from "./SelectIndexersDialog"

const CONN: AppConnection = {
  id: 10,
  name: "sonarr-main",
  kind: "sonarr",
  baseUrl: "http://sonarr:8989",
  harbrrUrl: "http://harbrr:7478",
  enabled: true,
  syncLevel: "full",
  indexScope: "selected",
  freeleechMode: "honor",
  syncProfileId: null,
  createdAt: "2026-07-01T00:00:00Z",
  updatedAt: "2026-07-01T00:00:00Z",
}

const INDEXERS: Instance[] = [
  {
    id: 1,
    slug: "tracker-a",
    definitionId: "tracker-a",
    name: "tracker-a",
    enabled: true,
    protocol: "torrent",
    proxyId: null,
    solverId: null,
    freeleech: false,
    priority: 25,
    minSeeders: 0,
    createdAt: "2026-07-01T00:00:00Z",
    updatedAt: "2026-07-01T00:00:00Z",
  },
  {
    id: 2,
    slug: "tracker-b",
    definitionId: "tracker-b",
    name: "tracker-b",
    enabled: true,
    protocol: "torrent",
    proxyId: null,
    solverId: null,
    freeleech: false,
    priority: 25,
    minSeeders: 0,
    createdAt: "2026-07-01T00:00:00Z",
    updatedAt: "2026-07-01T00:00:00Z",
  },
]

// Five indexers already selected server-side; the dialog must never Save a
// full-replace built from anything less than this loaded set.
const STATUS: ConnectionStatus = {
  ...CONN,
  indexers: [
    { instanceId: 1, selected: true },
    { instanceId: 2, selected: true },
    { instanceId: 3, selected: true },
    { instanceId: 4, selected: true },
    { instanceId: 5, selected: true },
  ],
}

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } })
}

function wrap(children: ReactNode) {
  return (
    <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      {children}
    </QueryClientProvider>
  )
}

// statusImpl controls the /status response per test: a resolved value, a
// rejected/error response, or a promise that never settles (simulating the
// in-flight window before the query resolves).
function stubFetch(statusImpl: () => Promise<Response>) {
  vi.stubGlobal("fetch", vi.fn((request: Request) => {
    if (request.url.includes("/status")) return statusImpl()
    return Promise.resolve(jsonResponse(INDEXERS))
  }))
}

describe("SelectIndexersDialog selection-wipe guard", () => {
  afterEach(() => vi.unstubAllGlobals())

  it("disables checkboxes and Save while the current selection is still loading", async () => {
    stubFetch(() => new Promise(() => {})) // never resolves
    render(wrap(
      <SelectIndexersDialog conn={CONN} pending={false} onClose={vi.fn()} onSave={vi.fn()} />
    ))

    const checkbox = await screen.findByLabelText<HTMLButtonElement>("tracker-a")
    expect(checkbox.disabled).toBe(true)
    expect(screen.getByRole<HTMLButtonElement>("button", { name: "Save selection" }).disabled).toBe(true)
  })

  it("disables checkboxes and Save when the status fetch errors (the deterministic-wipe path)", async () => {
    stubFetch(() => Promise.resolve(jsonResponse({ error: "boom" }, 500)))
    render(wrap(
      <SelectIndexersDialog conn={CONN} pending={false} onClose={vi.fn()} onSave={vi.fn()} />
    ))

    const checkbox = await screen.findByLabelText<HTMLButtonElement>("tracker-a")
    // Give the rejected query a tick to settle into its error state.
    await waitFor(() => expect(checkbox.disabled).toBe(true))
    expect(screen.getByRole<HTMLButtonElement>("button", { name: "Save selection" }).disabled).toBe(true)
  })

  it("enables checkboxes and Save once the selection loads, and Save carries the full corrected set", async () => {
    stubFetch(() => Promise.resolve(jsonResponse(STATUS)))
    const onSave = vi.fn()
    render(wrap(
      <SelectIndexersDialog conn={CONN} pending={false} onClose={vi.fn()} onSave={onSave} />
    ))

    const checkboxA = await screen.findByLabelText<HTMLButtonElement>("tracker-a")
    const checkboxB = screen.getByLabelText<HTMLButtonElement>("tracker-b")
    await waitFor(() => expect(checkboxA.disabled).toBe(false))
    expect(checkboxB.disabled).toBe(false)

    // tracker-a (instanceId 1) starts checked (server-selected); uncheck it.
    expect(checkboxA.getAttribute("data-state")).toBe("checked")
    fireEvent.click(checkboxA)

    const save = screen.getByRole<HTMLButtonElement>("button", { name: "Save selection" })
    expect(save.disabled).toBe(false)
    fireEvent.click(save)

    // The rest of the loaded selection (2,3,4,5) must survive the toggle —
    // not a set seeded from empty or from just the clicked item.
    expect(onSave).toHaveBeenCalledWith(CONN.id, expect.arrayContaining([2, 3, 4, 5]))
    const [, savedIds] = onSave.mock.calls[0] as [number, number[]]
    expect(savedIds).not.toContain(1)
    expect(savedIds).toHaveLength(4)
  })
})
