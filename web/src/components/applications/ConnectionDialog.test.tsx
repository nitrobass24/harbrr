import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { fireEvent, render, screen } from "@testing-library/react"
import { afterEach, describe, expect, it, vi } from "vitest"
import type { ReactNode } from "react"
import type { AppConnection, SyncProfile } from "@/lib/api"
import { ConnectionDialog } from "./ConnectionDialog"

const PROFILES: SyncProfile[] = [
  {
    id: 4,
    name: "tv-profile",
    categories: [5000],
    minSeeders: 0,
    enableRss: true,
    enableAutomaticSearch: true,
    enableInteractiveSearch: true,
    createdAt: "2026-07-01T00:00:00Z",
    updatedAt: "2026-07-01T00:00:00Z",
  },
]

const SONARR_CONN: AppConnection = {
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
  syncProfileId: 4,
  createdAt: "2026-07-01T00:00:00Z",
  updatedAt: "2026-07-01T00:00:00Z",
}

const QUI_CONN: AppConnection = { ...SONARR_CONN, id: 11, name: "qui-main", kind: "qui", syncProfileId: null }

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

function stubFetch() {
  vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse(PROFILES)))
}

describe("ConnectionDialog sync profile picker", () => {
  afterEach(() => vi.unstubAllGlobals())

  it("hides the picker for a qui connection", async () => {
    stubFetch()
    render(wrap(
      <ConnectionDialog
        state={{ open: true, existing: QUI_CONN }}
        pending={false}
        error={null}
        onClose={vi.fn()}
        onCreate={vi.fn()}
        onUpdate={vi.fn()}
      />
    ))

    await screen.findByRole("dialog")
    expect(screen.queryByLabelText("Sync profile")).toBeNull()
  })

  it("shows the picker for a sonarr connection", async () => {
    stubFetch()
    render(wrap(
      <ConnectionDialog
        state={{ open: true, existing: SONARR_CONN }}
        pending={false}
        error={null}
        onClose={vi.fn()}
        onCreate={vi.fn()}
        onUpdate={vi.fn()}
      />
    ))

    expect(await screen.findByLabelText("Sync profile")).toBeTruthy()
  })

  it("create: a selected profile rides the create body as syncProfileId", async () => {
    stubFetch()
    const onCreate = vi.fn()
    render(wrap(
      <ConnectionDialog
        state={{ open: true }}
        pending={false}
        error={null}
        onClose={vi.fn()}
        onCreate={onCreate}
        onUpdate={vi.fn()}
      />
    ))

    // Fill the required create fields (kind defaults to sonarr, so the picker shows).
    fireEvent.change(await screen.findByLabelText("Name"), { target: { value: "sonarr-new" } })
    fireEvent.change(screen.getByLabelText("App base URL"), { target: { value: "http://sonarr:8989" } })
    fireEvent.change(screen.getByLabelText("App API key"), { target: { value: "app-key" } })

    const select = await screen.findByLabelText("Sync profile")
    await screen.findByRole("option", { name: "tv-profile" })
    fireEvent.change(select, { target: { value: "4" } })
    fireEvent.click(screen.getByRole("button", { name: "Add application" }))

    expect(onCreate).toHaveBeenCalledWith(expect.objectContaining({ name: "sonarr-new", syncProfileId: 4 }))
  })

  it("edit: selecting None for an existing profile submits syncProfileId: null", async () => {
    stubFetch()
    const onUpdate = vi.fn()
    render(wrap(
      <ConnectionDialog
        state={{ open: true, existing: SONARR_CONN }}
        pending={false}
        error={null}
        onClose={vi.fn()}
        onCreate={vi.fn()}
        onUpdate={onUpdate}
      />
    ))

    const select = await screen.findByLabelText("Sync profile")
    // Wait for the profiles fetch to land before asserting the seeded value —
    // until its <option value="4"> exists, the controlled select reads back "".
    await screen.findByRole("option", { name: "tv-profile" })
    expect((select as HTMLSelectElement).value).toBe("4")

    fireEvent.change(select, { target: { value: "" } })
    fireEvent.click(screen.getByRole("button", { name: "Save changes" }))

    expect(onUpdate).toHaveBeenCalledWith(SONARR_CONN.id, expect.objectContaining({ syncProfileId: null }))
  })
})

describe("ConnectionDialog freeleech mode", () => {
  afterEach(() => vi.unstubAllGlobals())

  it("edit: selecting 'default by kind' for an *arr resolves to the concrete honor default, not undefined", async () => {
    stubFetch()
    const onUpdate = vi.fn()
    render(wrap(
      <ConnectionDialog
        state={{ open: true, existing: { ...SONARR_CONN, freeleechMode: "bypass" } }}
        pending={false}
        error={null}
        onClose={vi.fn()}
        onCreate={vi.fn()}
        onUpdate={onUpdate}
      />
    ))

    const select = await screen.findByLabelText("Freeleech feed")
    expect((select as HTMLSelectElement).value).toBe("bypass")

    // Choosing "default by kind" must be HONORED, not silently dropped: the PATCH omits an
    // undefined field, so the client resolves it to the kind's concrete default (honor for *arr).
    fireEvent.change(select, { target: { value: "" } })
    fireEvent.click(screen.getByRole("button", { name: "Save changes" }))

    expect(onUpdate).toHaveBeenCalledWith(SONARR_CONN.id, expect.objectContaining({ freeleechMode: "honor" }))
  })

  it("edit: selecting 'default by kind' for a qui connection resolves to the bypass default", async () => {
    stubFetch()
    const onUpdate = vi.fn()
    render(wrap(
      <ConnectionDialog
        state={{ open: true, existing: { ...QUI_CONN, freeleechMode: "honor" } }}
        pending={false}
        error={null}
        onClose={vi.fn()}
        onCreate={vi.fn()}
        onUpdate={onUpdate}
      />
    ))

    const select = await screen.findByLabelText("Freeleech feed")
    fireEvent.change(select, { target: { value: "" } })
    fireEvent.click(screen.getByRole("button", { name: "Save changes" }))

    expect(onUpdate).toHaveBeenCalledWith(QUI_CONN.id, expect.objectContaining({ freeleechMode: "bypass" }))
  })
})
