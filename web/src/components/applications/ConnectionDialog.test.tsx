import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { fireEvent, render, screen } from "@testing-library/react"
import { afterEach, describe, expect, it, vi } from "vitest"
import type { ReactNode } from "react"
import type { App, AppConnection, SyncProfile } from "@/lib/api"
import { ConnectionDialog } from "./ConnectionDialog"

const SONARR_APP: App = {
  id: 3, kind: "sonarr", name: "sonarr-app", baseUrl: "http://sonarr:8989", username: "",
  apiKey: "<redacted>", harbrrUrl: "http://harbrr:7478", enabled: true,
  references: { appConnections: 0, announce: 0, download: 0 },
  createdAt: "2026-07-01T00:00:00Z", updatedAt: "2026-07-01T00:00:00Z",
}

const PROFILES: SyncProfile[] = [
  {
    id: 4,
    name: "tv-profile",
    indexerIds: [1],
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
  freeleechMode: "honor",
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

// A fresh Response per call: the form now fires two concurrent GETs (sync profiles +
// apps), and a Response body can only be read once — mockResolvedValue would hand both
// callers the same singleton Response, so the second .json() throws.
function stubFetch() {
  vi.stubGlobal("fetch", vi.fn().mockImplementation(() => Promise.resolve(jsonResponse(PROFILES))))
}

describe("ConnectionDialog sync profile picker", () => {
  afterEach(() => vi.unstubAllGlobals())

  it("shows the picker for a qui connection too (#365 dropped the qui rejection)", async () => {
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

    expect(await screen.findByLabelText("Sync profile")).toBeTruthy()
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

describe("ConnectionDialog create — App picker", () => {
  afterEach(() => vi.unstubAllGlobals())

  function stubFetchWithApp() {
    vi.stubGlobal("fetch", vi.fn((request: Request) => {
      if (request.url.includes("/apps")) return Promise.resolve(jsonResponse([SONARR_APP]))
      return Promise.resolve(jsonResponse(PROFILES))
    }))
  }

  it("flipped default: an existing app of the current kind pre-selects — inline fields start hidden", async () => {
    stubFetchWithApp()
    render(wrap(
      <ConnectionDialog state={{ open: true }} pending={false} error={null} onClose={vi.fn()} onCreate={vi.fn()} onUpdate={vi.fn()} />
    ))

    const appSelect = await screen.findByLabelText<HTMLSelectElement>("App")
    await screen.findByRole("option", { name: "sonarr-app (sonarr)" })
    // No interaction — the picker itself defaults to the existing app.
    expect(appSelect.value).toBe(String(SONARR_APP.id))
    expect(screen.queryByLabelText("App base URL")).toBeNull()

    // "New app…" is the last option and, once chosen, brings the inline fields back.
    const options = Array.from(appSelect.options)
    expect(options[options.length - 1].textContent).toBe("New app…")
    fireEvent.change(appSelect, { target: { value: "new" } })
    expect(screen.getByLabelText("App base URL")).toBeTruthy()
  })

  it("picking an existing app hides the inline fields and submits appId", async () => {
    stubFetchWithApp()
    const onCreate = vi.fn()
    render(wrap(
      <ConnectionDialog state={{ open: true }} pending={false} error={null} onClose={vi.fn()} onCreate={onCreate} onUpdate={vi.fn()} />
    ))

    const appSelect = await screen.findByLabelText("App")
    await screen.findByRole("option", { name: "sonarr-app (sonarr)" })
    fireEvent.change(appSelect, { target: { value: String(SONARR_APP.id) } })

    expect(screen.queryByLabelText("App base URL")).toBeNull()
    fireEvent.change(screen.getByLabelText("Name"), { target: { value: "sonarr-new" } })
    fireEvent.click(screen.getByRole("button", { name: "Add application" }))

    expect(onCreate).toHaveBeenCalledWith(expect.objectContaining({ name: "sonarr-new", kind: "sonarr", appId: SONARR_APP.id }))
    expect(onCreate.mock.calls[0][0]).not.toHaveProperty("baseUrl")
  })

  it("no app: switching to 'New app…' reveals the inline fields and the create submits them", async () => {
    stubFetchWithApp()
    const onCreate = vi.fn()
    render(wrap(
      <ConnectionDialog state={{ open: true }} pending={false} error={null} onClose={vi.fn()} onCreate={onCreate} onUpdate={vi.fn()} />
    ))

    const appSelect = await screen.findByLabelText("App")
    await screen.findByRole("option", { name: "sonarr-app (sonarr)" })
    fireEvent.change(appSelect, { target: { value: "new" } })
    expect(screen.getByLabelText("App base URL")).toBeTruthy()

    fireEvent.change(screen.getByLabelText("Name"), { target: { value: "sonarr-new" } })
    fireEvent.change(screen.getByLabelText("App base URL"), { target: { value: "http://sonarr:8989" } })
    fireEvent.change(screen.getByLabelText("App API key"), { target: { value: "app-key" } })
    fireEvent.click(screen.getByRole("button", { name: "Add application" }))

    expect(onCreate).toHaveBeenCalledWith(expect.objectContaining({
      name: "sonarr-new", kind: "sonarr", baseUrl: "http://sonarr:8989", apiKey: "app-key",
    }))
    expect(onCreate.mock.calls[0][0]).not.toHaveProperty("appId")
  })
})

describe("ConnectionDialog create — Already configured block", () => {
  afterEach(() => vi.unstubAllGlobals())

  function stubFetchWithApps(apps: App[]) {
    vi.stubGlobal("fetch", vi.fn((request: Request) => {
      if (request.url.includes("/apps")) return Promise.resolve(jsonResponse(apps))
      return Promise.resolve(jsonResponse(PROFILES))
    }))
  }

  it("renders only when compatible Apps exist", async () => {
    stubFetchWithApps([SONARR_APP])
    render(wrap(
      <ConnectionDialog state={{ open: true }} pending={false} error={null} onClose={vi.fn()} onCreate={vi.fn()} onUpdate={vi.fn()} />
    ))

    expect(await screen.findByText("Already configured")).toBeTruthy()
  })

  it("renders nothing when no compatible App exists", async () => {
    stubFetchWithApps([])
    render(wrap(
      <ConnectionDialog state={{ open: true }} pending={false} error={null} onClose={vi.fn()} onCreate={vi.fn()} onUpdate={vi.fn()} />
    ))

    await screen.findByLabelText("Name")
    expect(screen.queryByText("Already configured")).toBeNull()
  })

  it("picking a row pre-selects kind + app, hides identity fields, shows the reuse hint, and prefills Name", async () => {
    const radarrApp: App = { ...SONARR_APP, id: 8, kind: "radarr", name: "radarr-app" }
    stubFetchWithApps([radarrApp])
    render(wrap(
      <ConnectionDialog state={{ open: true }} pending={false} error={null} onClose={vi.fn()} onCreate={vi.fn()} onUpdate={vi.fn()} />
    ))

    fireEvent.click(await screen.findByRole("button", { name: /radarr-app/ }))

    expect((screen.getByLabelText<HTMLSelectElement>("Kind")).value).toBe("radarr")
    expect(screen.queryByLabelText("App base URL")).toBeNull()
    expect(await screen.findByText(/Reusing/)).toBeTruthy()
    expect(screen.getByLabelText<HTMLInputElement>("Name").value).toBe("radarr-app")
  })

  it("does not overwrite a Name the operator already typed", async () => {
    stubFetchWithApps([SONARR_APP])
    render(wrap(
      <ConnectionDialog state={{ open: true }} pending={false} error={null} onClose={vi.fn()} onCreate={vi.fn()} onUpdate={vi.fn()} />
    ))

    fireEvent.change(await screen.findByLabelText("Name"), { target: { value: "custom-name" } })
    fireEvent.click(screen.getByRole("button", { name: /sonarr-app/ }))

    expect(screen.getByLabelText<HTMLInputElement>("Name").value).toBe("custom-name")
  })

  it("only a used app of the kind: the picker defaults to 'New app…' with inline fields visible", async () => {
    const usedApp: App = { ...SONARR_APP, references: { appConnections: 1, announce: 0, download: 0 } }
    stubFetchWithApps([usedApp])
    render(wrap(
      <ConnectionDialog state={{ open: true }} pending={false} error={null} onClose={vi.fn()} onCreate={vi.fn()} onUpdate={vi.fn()} />
    ))

    const appSelect = await screen.findByLabelText<HTMLSelectElement>("App")
    // A used app would 409 on create (one sync row per App), so it is never the
    // default and its option is disabled, marked "already added".
    const option = await screen.findByRole<HTMLOptionElement>("option", { name: /sonarr-app \(sonarr\) — already added/ })
    expect(option.disabled).toBe(true)
    expect(appSelect.value).toBe("new")
    expect(screen.getByLabelText("App base URL")).toBeTruthy()
  })

  it("disables a row already used by app-sync, with an 'already added' note", async () => {
    const usedApp: App = { ...SONARR_APP, references: { appConnections: 1, announce: 0, download: 0 } }
    stubFetchWithApps([usedApp])
    render(wrap(
      <ConnectionDialog state={{ open: true }} pending={false} error={null} onClose={vi.fn()} onCreate={vi.fn()} onUpdate={vi.fn()} />
    ))

    const row = await screen.findByRole<HTMLButtonElement>("button", { name: /sonarr-app/ })
    expect(row.disabled).toBe(true)
    expect(await screen.findByText("already added")).toBeTruthy()
  })

  it("create-via-block-pick posts appId, not inline identity", async () => {
    stubFetchWithApps([SONARR_APP])
    const onCreate = vi.fn()
    render(wrap(
      <ConnectionDialog state={{ open: true }} pending={false} error={null} onClose={vi.fn()} onCreate={onCreate} onUpdate={vi.fn()} />
    ))

    fireEvent.click(await screen.findByRole("button", { name: /sonarr-app/ }))
    fireEvent.click(screen.getByRole("button", { name: "Add application" }))

    expect(onCreate).toHaveBeenCalledWith(expect.objectContaining({ name: "sonarr-app", kind: "sonarr", appId: SONARR_APP.id }))
    expect(onCreate.mock.calls[0][0]).not.toHaveProperty("baseUrl")
  })
})
