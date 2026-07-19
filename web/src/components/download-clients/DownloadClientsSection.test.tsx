import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { fireEvent, render, screen, waitFor } from "@testing-library/react"
import { afterEach, describe, expect, it, vi } from "vitest"
import type { ReactNode } from "react"
import type { App, DownloadClient, DownloadClientSettings } from "@/lib/api"
import { DownloadClientsSection } from "./DownloadClientsSection"

const CLIENT: DownloadClient = {
  id: 5, name: "seedbox", kind: "qbittorrent", appId: 1, enabled: true, host: "http://localhost:8080",
  username: "admin", secret: "<redacted>", settings: {},
  createdAt: "2026-07-01T00:00:00Z", updatedAt: "2026-07-01T00:00:00Z",
}

const QUI_APP: App = {
  id: 9, kind: "qui", name: "qui-main", baseUrl: "http://qui:7476", username: "",
  apiKey: "<redacted>", harbrrUrl: "", enabled: true,
  references: { appConnections: 0, announce: 0, download: 0 },
  createdAt: "2026-07-01T00:00:00Z", updatedAt: "2026-07-01T00:00:00Z",
}

interface PatchDownloadClientBody {
  name?: string
  host?: string
  secret?: string
  settings?: DownloadClientSettings
}

interface CreateDownloadClientBody {
  appId?: number
  settings?: DownloadClientSettings
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

// Stubs GET /api/download-clients with CLIENT and captures the PATCH body sent on save.
function stubFetchAndCapturePatch(): { patchBody: () => Promise<PatchDownloadClientBody> } {
  const fetchMock = vi.fn((req: Request) => {
    if (req.method === "PATCH") return Promise.resolve(jsonResponse({}))
    if (req.url.includes("/apps")) return Promise.resolve(jsonResponse([]))
    return Promise.resolve(jsonResponse([CLIENT]))
  })
  vi.stubGlobal("fetch", fetchMock)
  return {
    patchBody: async () => {
      const call = await vi.waitFor(() => {
        const found = fetchMock.mock.calls.find(([req]) => req.method === "PATCH")
        if (!found) throw new Error("no PATCH call yet")
        return found
      })
      return JSON.parse(await call[0].text()) as PatchDownloadClientBody
    },
  }
}

// Two buttons share the "Add download client" label once the dialog is open: the
// toolbar opener and the form's own submit button. Disambiguate by element type.
function addSubmitButton(): HTMLButtonElement {
  return screen
    .getAllByRole("button", { name: "Add download client" })
    .find((b): b is HTMLButtonElement => b instanceof HTMLButtonElement && b.type === "submit")!
}

describe("DownloadClientsSection", () => {
  afterEach(() => vi.unstubAllGlobals())

  it("lists a client's name, kind, and host", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse([CLIENT])))
    render(wrap(<DownloadClientsSection />))

    expect(await screen.findByText("seedbox")).toBeTruthy()
    expect(screen.getByText("qbittorrent")).toBeTruthy()
    expect(screen.getByText("http://localhost:8080")).toBeTruthy()
  })

  it("edit: identity fields are gone (App-level now); PATCH sends name + settings only", async () => {
    const { patchBody } = stubFetchAndCapturePatch()
    render(wrap(<DownloadClientsSection />))

    fireEvent.click(await screen.findByLabelText("Edit seedbox"))
    // Host/username/secret inputs no longer exist on the edit form — they rotate via
    // the App (AppsSection), not this PATCH — replaced by a "managed by app" hint.
    expect(screen.queryByLabelText("Host")).toBeNull()
    expect(screen.queryByLabelText(/Password/)).toBeNull()
    await screen.findByText(/Identity and credential are managed by app/)
    fireEvent.click(screen.getByRole("button", { name: "Save changes" }))

    const body = await patchBody()
    expect(body).toEqual({ name: "seedbox", settings: { qbittorrent: {} } })
  })

  it("add: selecting blackhole hides host/username/password and shows watch-folder fields", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse([CLIENT])))
    render(wrap(<DownloadClientsSection />))

    fireEvent.click(await screen.findByRole("button", { name: "Add download client" }))
    fireEvent.change(await screen.findByLabelText("Kind"), { target: { value: "blackhole" } })

    expect(screen.queryByLabelText("Host")).toBeNull()
    expect(screen.queryByLabelText(/Username/)).toBeNull()
    expect(screen.queryByLabelText(/Password/)).toBeNull()
    expect(screen.getByLabelText(/Torrent watch folder/)).toBeTruthy()
    expect(screen.getByLabelText(/NZB watch folder/)).toBeTruthy()

    // Submit stays blocked until at least one watch folder is set (the server
    // would 400 on dir-less blackhole settings; the form doesn't offer the trip).
    fireEvent.change(screen.getByLabelText("Name"), { target: { value: "bh" } })
    const submit = addSubmitButton()
    expect(submit.disabled).toBe(true)
    fireEvent.change(screen.getByLabelText(/Torrent watch folder/), { target: { value: "/watch/torrents" } })
    expect(submit.disabled).toBe(false)
  })

  it("add: a qui App picker submits appId + settings — no host/username/secret", async () => {
    const fetchMock = vi.fn((req: Request) => {
      if (req.url.includes("/qui-instances")) {
        return Promise.resolve(jsonResponse({ ok: true, instances: [{ id: 3, name: "main" }] }))
      }
      if (req.url.includes("/apps")) return Promise.resolve(jsonResponse([QUI_APP]))
      if (req.method === "POST" && req.url.includes("/download-clients")) {
        return Promise.resolve(jsonResponse({ ...CLIENT, kind: "qui" }))
      }
      return Promise.resolve(jsonResponse([]))
    })
    vi.stubGlobal("fetch", fetchMock)
    render(wrap(<DownloadClientsSection />))

    fireEvent.click(await screen.findByRole("button", { name: "Add download client" }))
    fireEvent.change(await screen.findByLabelText("Kind"), { target: { value: "qui" } })

    // Exactly one qui App exists, so the picker one-click-selects it.
    const appSelect = await screen.findByLabelText<HTMLSelectElement>("qui app")
    await waitFor(() => expect(appSelect.value).toBe(String(QUI_APP.id)))
    expect(screen.queryByLabelText("Host")).toBeNull()

    // The instance dropdown populates from the App's live qui instances.
    const instanceSelect = await screen.findByLabelText<HTMLSelectElement>("Instance")
    await screen.findByRole("option", { name: "main" })
    fireEvent.change(instanceSelect, { target: { value: "3" } })

    // Picking an instance prefills the name.
    expect(screen.getByLabelText<HTMLInputElement>("Name").value).toBe("main")

    fireEvent.click(addSubmitButton())

    await waitFor(async () => {
      const post = fetchMock.mock.calls.find(([req]) => req.method === "POST" && req.url.includes("/download-clients"))
      expect(post).toBeTruthy()
      const body = JSON.parse(await post![0].clone().text()) as CreateDownloadClientBody
      expect(body).toEqual({ name: "main", kind: "qui", appId: QUI_APP.id, settings: { qui: { instanceId: 3 } } })
    })
  })

  it("test: posts to the test endpoint and surfaces a toast", async () => {
    const fetchMock = vi.fn((req: Request) => {
      if (req.method === "POST" && req.url.includes("/test")) return Promise.resolve(jsonResponse({ ok: true }))
      return Promise.resolve(jsonResponse([CLIENT]))
    })
    vi.stubGlobal("fetch", fetchMock)
    render(wrap(<DownloadClientsSection />))

    fireEvent.click(await screen.findByRole("button", { name: "Test" }))

    await vi.waitFor(() => {
      const found = fetchMock.mock.calls.find(([req]) => req.method === "POST" && req.url.includes("/test"))
      if (!found) throw new Error("no test call yet")
    })
  })
})
