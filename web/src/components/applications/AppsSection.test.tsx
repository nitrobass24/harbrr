import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { fireEvent, render, screen, waitFor, within } from "@testing-library/react"
import { afterEach, describe, expect, it, vi } from "vitest"
import type { ReactNode } from "react"
import { withMemoryRouter } from "@/test/router"
import type { App } from "@/lib/api"
import { AppsSection } from "./AppsSection"

const APP: App = {
  id: 3, kind: "sonarr", name: "sonarr-app", baseUrl: "http://sonarr:8989", username: "",
  apiKey: "<redacted>", harbrrUrl: "http://harbrr:7478", enabled: true,
  references: { appConnections: 2, announce: 0, download: 1 },
  createdAt: "2026-07-01T00:00:00Z", updatedAt: "2026-07-01T00:00:00Z",
}

// A fresh qui App: compatible with all three surfaces (ConnectionDialog's KINDS,
// AnnounceSection's qui/crossseed-v6, DownloadClientsSection's qui-only reuse path)
// and unused by any of them, so "Use as…" offers all three.
const QUI_FRESH: App = {
  id: 11, kind: "qui", name: "qui-fresh", baseUrl: "http://qui:7476", username: "",
  apiKey: "<redacted>", harbrrUrl: "http://harbrr:7478", enabled: true,
  references: { appConnections: 0, announce: 0, download: 0 },
  createdAt: "2026-07-01T00:00:00Z", updatedAt: "2026-07-01T00:00:00Z",
}

// A qui App that already has an app-sync row: "Sync target" should be hidden (one row
// per App), but "Announce target" and "Download client" stay offered — download in
// particular has no uniqueness rule (multiple clients per App are legal).
const QUI_SYNCED: App = {
  id: 12, kind: "qui", name: "qui-synced", baseUrl: "http://qui2:7476", username: "",
  apiKey: "<redacted>", harbrrUrl: "http://harbrr:7478", enabled: true,
  references: { appConnections: 1, announce: 0, download: 0 },
  createdAt: "2026-07-01T00:00:00Z", updatedAt: "2026-07-01T00:00:00Z",
}

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(status === 204 ? null : JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  })
}

function wrap(children: ReactNode) {
  return (
    <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      {children}
    </QueryClientProvider>
  )
}

// AppsSection's "Use as…" items navigate via the router — needs router context, so
// this is rendered inside the shared memory-router harness (whose "/applications" and
// "/download-clients" routes echo their search params back for assertions).
function renderWithRouter(ui: ReactNode) {
  return render(wrap(withMemoryRouter(ui)))
}

// Radix's DropdownMenuTrigger opens on pointerdown, not click — jsdom has no
// PointerEvent, so a plain fireEvent.click leaves it closed. Fire both.
function openUseAsMenu(appName: string) {
  const trigger = screen.getByRole("button", { name: `Use ${appName} as…` })
  fireEvent.pointerDown(trigger, { button: 0, pointerId: 1 })
  fireEvent.click(trigger)
}

describe("AppsSection", () => {
  afterEach(() => vi.unstubAllGlobals())

  it("lists an app's name, kind, and reference count", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse([APP])))
    render(wrap(<AppsSection />))

    expect(await screen.findByText("sonarr-app")).toBeTruthy()
    expect(screen.getByText("sonarr")).toBeTruthy()
    expect(screen.getByText(/used by 3 surfaces/)).toBeTruthy()
  })

  it("edit: a typed credential rotates it; a blank one keeps the stored one", async () => {
    const fetchMock = vi.fn((request: Request) => {
      if (request.method === "PATCH") return Promise.resolve(jsonResponse(null, 204))
      return Promise.resolve(jsonResponse([APP]))
    })
    vi.stubGlobal("fetch", fetchMock)
    render(wrap(<AppsSection />))

    fireEvent.click(await screen.findByLabelText("Edit sonarr-app"))
    fireEvent.click(await screen.findByRole("button", { name: "Save changes" }))

    await waitFor(async () => {
      const patch = fetchMock.mock.calls.find(([request]) => request.method === "PATCH")
      expect(patch).toBeTruthy()
      const body = JSON.parse(await patch![0].clone().text()) as Record<string, unknown>
      expect(body).not.toHaveProperty("apiKey")
      expect(body).toMatchObject({ name: "sonarr-app", baseUrl: "http://sonarr:8989" })
    })
  })

  it("edit: a typed credential is sent as apiKey", async () => {
    const fetchMock = vi.fn((request: Request) => {
      if (request.method === "PATCH") return Promise.resolve(jsonResponse(null, 204))
      return Promise.resolve(jsonResponse([APP]))
    })
    vi.stubGlobal("fetch", fetchMock)
    render(wrap(<AppsSection />))

    fireEvent.click(await screen.findByLabelText("Edit sonarr-app"))
    fireEvent.change(await screen.findByLabelText(/Credential/), { target: { value: "new-key" } })
    fireEvent.click(await screen.findByRole("button", { name: "Save changes" }))

    await waitFor(async () => {
      const patch = fetchMock.mock.calls.find(([request]) => request.method === "PATCH")
      expect(patch).toBeTruthy()
      const body = JSON.parse(await patch![0].clone().text()) as Record<string, unknown>
      expect(body).toMatchObject({ apiKey: "new-key" })
    })
  })

  // The 409 conflict message itself is covered by useApps.test.tsx (useDeleteApp
  // owns the toast); this just confirms the row wires the click through to DELETE.
  it("delete: posts to the delete endpoint", async () => {
    const fetchMock = vi.fn((request: Request) => {
      if (request.method === "DELETE") {
        return Promise.resolve(jsonResponse({ error: "app is in use by 2 app-sync connections, 1 download client", code: "conflict" }, 409))
      }
      return Promise.resolve(jsonResponse([APP]))
    })
    vi.stubGlobal("fetch", fetchMock)
    render(wrap(<AppsSection />))

    fireEvent.click(await screen.findByLabelText("Delete sonarr-app"))

    await waitFor(() => {
      const del = fetchMock.mock.calls.find(([request]) => request.method === "DELETE")
      expect(del).toBeTruthy()
    })
  })

  // autobrr/harbrr#300 — "Use as…" actions.
  describe("Use as…", () => {
    it("offers nothing for a kind/uniqueness-incompatible app (no dropdown at all)", async () => {
      // sonarr is not announce/download-compatible, and this app's sync row already
      // exists — every candidate surface is filtered out, so the menu doesn't render.
      vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse([APP])))
      renderWithRouter(<AppsSection />)

      await screen.findByText("sonarr-app")
      expect(screen.queryByRole("button", { name: /Use sonarr-app as…/ })).toBeNull()
    })

    it("hides an already-used surface but keeps offering the rest (download always offered)", async () => {
      vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse([QUI_SYNCED])))
      renderWithRouter(<AppsSection />)

      await screen.findByText("qui-synced")
      openUseAsMenu("qui-synced")

      const menu = await screen.findByRole("menu")
      expect(within(menu).queryByRole("menuitem", { name: "Sync target" })).toBeNull()
      expect(within(menu).getByRole("menuitem", { name: "Announce target" })).toBeTruthy()
      expect(within(menu).getByRole("menuitem", { name: "Download client" })).toBeTruthy()
    })

    it("offers every compatible surface for a fresh app", async () => {
      vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse([QUI_FRESH])))
      renderWithRouter(<AppsSection />)

      await screen.findByText("qui-fresh")
      openUseAsMenu("qui-fresh")

      const menu = await screen.findByRole("menu")
      expect(within(menu).getByRole("menuitem", { name: "Sync target" })).toBeTruthy()
      expect(within(menu).getByRole("menuitem", { name: "Announce target" })).toBeTruthy()
      expect(within(menu).getByRole("menuitem", { name: "Download client" })).toBeTruthy()
    })

    it("Sync target navigates to /applications with create=sync and the app's id", async () => {
      vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse([QUI_FRESH])))
      renderWithRouter(<AppsSection />)

      await screen.findByText("qui-fresh")
      openUseAsMenu("qui-fresh")
      fireEvent.click(await screen.findByRole("menuitem", { name: "Sync target" }))

      const landed = await screen.findByTestId("echo-applications")
      expect(JSON.parse(landed.textContent ?? "")).toEqual({ create: "sync", appId: 11 })
    })

    it("Download client navigates to /download-clients with just the app's id (no `create`)", async () => {
      vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse([QUI_FRESH])))
      renderWithRouter(<AppsSection />)

      await screen.findByText("qui-fresh")
      openUseAsMenu("qui-fresh")
      fireEvent.click(await screen.findByRole("menuitem", { name: "Download client" }))

      const landed = await screen.findByTestId("echo-download-clients")
      expect(JSON.parse(landed.textContent ?? "")).toEqual({ appId: 11 })
    })
  })
})
