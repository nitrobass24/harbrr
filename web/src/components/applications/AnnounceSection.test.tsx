import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { fireEvent, render, screen, waitFor } from "@testing-library/react"
import { afterEach, describe, expect, it, vi } from "vitest"
import type { ReactNode } from "react"
import type { AnnounceConnection, App } from "@/lib/api"
import { AnnounceSection } from "./AnnounceSection"

const TARGET: AnnounceConnection = {
  id: 1,
  name: "qui-main",
  kind: "qui",
  baseUrl: "http://qui:7476",
  harbrrUrl: "http://harbrr:7478",
  apiKey: "<redacted>",
  enabled: true,
  createdAt: "2026-07-01T00:00:00Z",
  updatedAt: "2026-07-01T00:00:00Z",
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

describe("AnnounceSection edit", () => {
  afterEach(() => vi.unstubAllGlobals())

  it("edit is name-only: identity/credential are App-level now, not resubmitted", async () => {
    const fetchMock = vi.fn((request: Request) => {
      if (request.method === "PATCH" && request.url.includes("/announce-connections")) {
        return Promise.resolve(jsonResponse(null, 204))
      }
      if (request.url.includes("/announce-connections")) return Promise.resolve(jsonResponse([TARGET]))
      if (request.url.includes("/server-info")) return Promise.resolve(jsonResponse({ port: 7478 }))
      return Promise.resolve(jsonResponse([]))
    })
    vi.stubGlobal("fetch", fetchMock)

    render(wrap(<AnnounceSection />))

    fireEvent.click(await screen.findByRole("button", { name: "Edit qui-main" }))
    // The edit form is seeded from the existing target; base URL/API key/harbrr URL
    // inputs are gone (those now rotate via the App, not this PATCH).
    const nameInput = await screen.findByLabelText<HTMLInputElement>("Name")
    expect(nameInput.value).toBe("qui-main")
    expect(screen.queryByLabelText("Tool base URL")).toBeNull()
    expect(screen.queryByLabelText("Tool API key")).toBeNull()
    fireEvent.click(screen.getByRole("button", { name: "Save changes" }))

    await waitFor(async () => {
      const patch = fetchMock.mock.calls.find(([request]) => request.method === "PATCH")
      expect(patch).toBeTruthy()
      const body = JSON.parse(await patch![0].clone().text()) as Record<string, unknown>
      expect(body).toEqual({ name: "qui-main" })
    })
  })
})

describe("AnnounceSection create — App picker", () => {
  afterEach(() => vi.unstubAllGlobals())

  const APP = {
    id: 7, kind: "qui", name: "qui-main-app", baseUrl: "http://qui:7476", username: "",
    apiKey: "<redacted>", harbrrUrl: "http://harbrr:7478", enabled: true,
    references: { appConnections: 0, announce: 0, download: 0 },
    createdAt: "2026-07-01T00:00:00Z", updatedAt: "2026-07-01T00:00:00Z",
  }

  it("picking an existing app hides the inline fields and submits appId", async () => {
    const fetchMock = vi.fn((request: Request) => {
      if (request.method === "POST" && request.url.includes("/announce-connections")) {
        return Promise.resolve(jsonResponse(TARGET, 201))
      }
      if (request.url.includes("/apps")) return Promise.resolve(jsonResponse([APP]))
      if (request.url.includes("/announce-connections")) return Promise.resolve(jsonResponse([]))
      if (request.url.includes("/server-info")) return Promise.resolve(jsonResponse({ port: 7478 }))
      return Promise.resolve(jsonResponse([]))
    })
    vi.stubGlobal("fetch", fetchMock)
    render(wrap(<AnnounceSection />))

    fireEvent.click(await screen.findByRole("button", { name: "Add target" }))
    const appSelect = await screen.findByLabelText("App")
    await screen.findByRole("option", { name: "qui-main-app (qui)" })
    fireEvent.change(appSelect, { target: { value: "7" } })

    expect(screen.queryByLabelText("Tool base URL")).toBeNull()
    fireEvent.change(screen.getByLabelText("Name"), { target: { value: "qui-target" } })
    fireEvent.click(submitButton())

    await waitFor(async () => {
      const post = fetchMock.mock.calls.find(([request]) => request.method === "POST")
      expect(post).toBeTruthy()
      const body = JSON.parse(await post![0].clone().text()) as Record<string, unknown>
      expect(body).toEqual({ name: "qui-target", kind: "qui", appId: 7 })
    })
  })

  it("no app: switching to 'New app…' reveals inline fields and the create submits them", async () => {
    const fetchMock = vi.fn((request: Request) => {
      if (request.method === "POST" && request.url.includes("/announce-connections")) {
        return Promise.resolve(jsonResponse(TARGET, 201))
      }
      if (request.url.includes("/apps")) return Promise.resolve(jsonResponse([APP]))
      if (request.url.includes("/announce-connections")) return Promise.resolve(jsonResponse([]))
      if (request.url.includes("/server-info")) return Promise.resolve(jsonResponse({ port: 7478 }))
      return Promise.resolve(jsonResponse([]))
    })
    vi.stubGlobal("fetch", fetchMock)
    render(wrap(<AnnounceSection />))

    fireEvent.click(await screen.findByRole("button", { name: "Add target" }))
    const appSelect = await screen.findByLabelText("App")
    // An App of this kind exists, so the picker defaults to it; switch to "New app…"
    // to exercise the inline-fields fallback.
    fireEvent.change(appSelect, { target: { value: "new" } })
    expect(screen.getByLabelText("Tool base URL")).toBeTruthy()
    expect(screen.getByLabelText("Tool API key")).toBeTruthy()

    fireEvent.change(screen.getByLabelText("Name"), { target: { value: "cs-target" } })
    fireEvent.change(screen.getByLabelText("Tool base URL"), { target: { value: "http://cross-seed:2468" } })
    fireEvent.change(screen.getByLabelText("Tool API key"), { target: { value: "cs-key" } })
    fireEvent.change(screen.getByLabelText("harbrr URL as the tool reaches it"), { target: { value: "http://harbrr:7478" } })
    fireEvent.click(submitButton())

    await waitFor(async () => {
      const post = fetchMock.mock.calls.find(([request]) => request.method === "POST")
      expect(post).toBeTruthy()
      const body = JSON.parse(await post![0].clone().text()) as Record<string, unknown>
      expect(body).toEqual({
        name: "cs-target", kind: "qui",
        baseUrl: "http://cross-seed:2468", apiKey: "cs-key", harbrrUrl: "http://harbrr:7478",
      })
    })
  })

  it("flipped default: the App picker defaults to the existing app without interaction", async () => {
    const fetchMock = vi.fn((request: Request) => {
      if (request.url.includes("/apps")) return Promise.resolve(jsonResponse([APP]))
      if (request.url.includes("/announce-connections")) return Promise.resolve(jsonResponse([]))
      if (request.url.includes("/server-info")) return Promise.resolve(jsonResponse({ port: 7478 }))
      return Promise.resolve(jsonResponse([]))
    })
    vi.stubGlobal("fetch", fetchMock)
    render(wrap(<AnnounceSection />))

    fireEvent.click(await screen.findByRole("button", { name: "Add target" }))
    const appSelect = await screen.findByLabelText<HTMLSelectElement>("App")
    await screen.findByRole("option", { name: "qui-main-app (qui)" })
    expect(appSelect.value).toBe(String(APP.id))
    expect(screen.queryByLabelText("Tool base URL")).toBeNull()
  })
})

describe("AnnounceSection create — Already configured block", () => {
  afterEach(() => vi.unstubAllGlobals())

  const APP: App = {
    id: 7, kind: "qui", name: "qui-main-app", baseUrl: "http://qui:7476", username: "",
    apiKey: "<redacted>", harbrrUrl: "http://harbrr:7478", enabled: true,
    references: { appConnections: 0, announce: 0, download: 0 },
    createdAt: "2026-07-01T00:00:00Z", updatedAt: "2026-07-01T00:00:00Z",
  }

  function stubFetchWithApps(apps: App[]) {
    vi.stubGlobal("fetch", vi.fn((request: Request) => {
      if (request.url.includes("/apps")) return Promise.resolve(jsonResponse(apps))
      if (request.url.includes("/announce-connections")) return Promise.resolve(jsonResponse([]))
      if (request.url.includes("/server-info")) return Promise.resolve(jsonResponse({ port: 7478 }))
      return Promise.resolve(jsonResponse([]))
    }))
  }

  it("renders only when compatible Apps exist", async () => {
    stubFetchWithApps([APP])
    render(wrap(<AnnounceSection />))
    fireEvent.click(await screen.findByRole("button", { name: "Add target" }))

    expect(await screen.findByText("Already configured")).toBeTruthy()
  })

  it("renders nothing when no compatible App exists", async () => {
    stubFetchWithApps([])
    render(wrap(<AnnounceSection />))
    fireEvent.click(await screen.findByRole("button", { name: "Add target" }))

    await screen.findByLabelText("Name")
    expect(screen.queryByText("Already configured")).toBeNull()
  })

  it("only a used app of the kind: the picker defaults to 'New app…' with inline fields visible", async () => {
    const usedApp: App = { ...APP, references: { appConnections: 0, announce: 1, download: 0 } }
    stubFetchWithApps([usedApp])
    render(wrap(<AnnounceSection />))
    fireEvent.click(await screen.findByRole("button", { name: "Add target" }))

    const appSelect = await screen.findByLabelText<HTMLSelectElement>("App")
    await screen.findByRole("option", { name: "qui-main-app (qui)" })
    // A used app would 409 on create (one announce row per App), so it is never the default.
    expect(appSelect.value).toBe("new")
    expect(screen.getByLabelText("Tool base URL")).toBeTruthy()
  })

  it("disables a row already used by an announce target", async () => {
    const usedApp: App = { ...APP, references: { appConnections: 0, announce: 1, download: 0 } }
    stubFetchWithApps([usedApp])
    render(wrap(<AnnounceSection />))
    fireEvent.click(await screen.findByRole("button", { name: "Add target" }))

    const row = await screen.findByRole<HTMLButtonElement>("button", { name: /qui-main-app/ })
    expect(row.disabled).toBe(true)
    expect(await screen.findByText("already added")).toBeTruthy()
  })
})

// Two buttons share the "Add target" label once the dialog is open: the toolbar
// opener and the form's own submit button. Disambiguate by element type.
function submitButton(): HTMLButtonElement {
  return screen
    .getAllByRole("button", { name: "Add target" })
    .find((b): b is HTMLButtonElement => b instanceof HTMLButtonElement && b.type === "submit")!
}
