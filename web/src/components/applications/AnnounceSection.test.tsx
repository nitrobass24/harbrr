import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { fireEvent, render, screen, waitFor } from "@testing-library/react"
import { describe, expect, it } from "vitest"
import type { ReactNode } from "react"
import type { AnnounceConnection, App } from "@/lib/api"
import { stubApi } from "@/test/stubApi"
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

function wrap(children: ReactNode) {
  return (
    <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      {children}
    </QueryClientProvider>
  )
}

describe("AnnounceSection edit", () => {
  it("edit is name-only: identity/credential are App-level now, not resubmitted", async () => {
    const api = stubApi({
      "GET /api/announce-connections": [TARGET],
      "PATCH /api/announce-connections/{id}": null,
      "GET /api/server-info": { port: 7478 },
      "GET /api/apps": [],
    })

    render(wrap(<AnnounceSection />))

    fireEvent.click(await screen.findByRole("button", { name: "Edit qui-main" }))
    // The edit form is seeded from the existing target; base URL/API key/harbrr URL
    // inputs are gone (those now rotate via the App, not this PATCH).
    const nameInput = await screen.findByLabelText<HTMLInputElement>("Name")
    expect(nameInput.value).toBe("qui-main")
    expect(screen.queryByLabelText("Host")).toBeNull()
    expect(screen.queryByLabelText("Tool API key")).toBeNull()
    fireEvent.click(screen.getByRole("button", { name: "Save changes" }))

    await waitFor(async () => {
      const patch = api.calls("PATCH /api/announce-connections/{id}")[0]
      expect(patch).toBeTruthy()
      const body = JSON.parse(await patch.clone().text()) as Record<string, unknown>
      expect(body).toEqual({ name: "qui-main" })
    })
  })
})

describe("AnnounceSection create — App picker", () => {
  const APP = {
    id: 7, kind: "qui", name: "qui-main-app", baseUrl: "http://qui:7476", username: "",
    apiKey: "<redacted>", harbrrUrl: "http://harbrr:7478", enabled: true,
    references: { appConnections: 0, announce: 0, download: 0 },
    createdAt: "2026-07-01T00:00:00Z", updatedAt: "2026-07-01T00:00:00Z",
  }

  const CROSSSEED_APP = { ...APP, id: 9, kind: "crossseed-v6", name: "cs-app", baseUrl: "http://cross-seed:2468" }

  it("picking an existing app hides the inline fields and submits appId", async () => {
    const api = stubApi({
      "POST /api/announce-connections": () => Response.json(TARGET, { status: 201 }),
      "GET /api/apps": [APP],
      "GET /api/announce-connections": [],
      "GET /api/server-info": { port: 7478 },
    })
    render(wrap(<AnnounceSection />))

    fireEvent.click(await screen.findByRole("button", { name: "Add target" }))
    const appSelect = await screen.findByLabelText("App")
    await screen.findByRole("option", { name: "qui-main-app (qui)" })
    fireEvent.change(appSelect, { target: { value: "7" } })

    expect(screen.queryByLabelText("Host")).toBeNull()
    fireEvent.change(screen.getByLabelText("Name"), { target: { value: "qui-target" } })
    fireEvent.click(submitButton())

    await waitFor(async () => {
      const post = api.calls("POST /api/announce-connections")[0]
      expect(post).toBeTruthy()
      const body = JSON.parse(await post.clone().text()) as Record<string, unknown>
      expect(body).toEqual({ name: "qui-target", kind: "qui", appId: 7 })
    })
  })

  it("no app: switching to 'New app…' reveals inline fields and the create submits them", async () => {
    const api = stubApi({
      "POST /api/announce-connections": () => Response.json(TARGET, { status: 201 }),
      "GET /api/apps": [APP],
      "GET /api/announce-connections": [],
      "GET /api/server-info": { port: 7478 },
    })
    render(wrap(<AnnounceSection />))

    fireEvent.click(await screen.findByRole("button", { name: "Add target" }))
    const appSelect = await screen.findByLabelText("App")
    // An App of this kind exists, so the picker defaults to it; switch to "New app…"
    // to exercise the inline-fields fallback.
    fireEvent.change(appSelect, { target: { value: "new" } })
    expect(screen.getByLabelText("Host")).toBeTruthy()
    expect(screen.getByLabelText("Tool API key")).toBeTruthy()

    fireEvent.change(screen.getByLabelText("Name"), { target: { value: "cs-target" } })
    // Kind stays "qui" (never switched) here, so its seeded port default (7476) doesn't
    // match this URL's port (2468) — paste the full URL so it fans out for real, the
    // way a real paste event does (typing doesn't fan out; that's onPaste-only).
    fireEvent.paste(screen.getByLabelText("Host"), { clipboardData: { getData: () => "http://cross-seed:2468" } })
    fireEvent.change(screen.getByLabelText("Tool API key"), { target: { value: "cs-key" } })
    fireEvent.change(screen.getByLabelText("harbrr URL as the tool reaches it"), { target: { value: "http://harbrr:7478" } })
    fireEvent.click(submitButton())

    await waitFor(async () => {
      const post = api.calls("POST /api/announce-connections")[0]
      expect(post).toBeTruthy()
      const body = JSON.parse(await post.clone().text()) as Record<string, unknown>
      expect(body).toEqual({
        name: "cs-target", kind: "qui",
        baseUrl: "http://cross-seed:2468", apiKey: "cs-key", harbrrUrl: "http://harbrr:7478",
      })
    })
  })

  it("deep-link pre-pick re-defaults the port for the picked App's kind, not the form's initial kind", async () => {
    stubApi({
      "GET /api/apps": [CROSSSEED_APP],
      "GET /api/announce-connections": [],
      "GET /api/server-info": { port: 7478 },
    })
    render(wrap(<AnnounceSection initialCreate={{ appId: CROSSSEED_APP.id }} />))

    // The deep-link pre-picks the cross-seed-v6 App (kind flips from the default "qui");
    // once the operator flips back to "New app…" the port must reflect cross-seed (2468),
    // not a leftover default from before the pick.
    const appSelect = await screen.findByLabelText<HTMLSelectElement>("App")
    await screen.findByRole("option", { name: "cs-app (cross-seed)" })
    fireEvent.change(appSelect, { target: { value: "new" } })

    expect(screen.getByLabelText<HTMLInputElement>("Port").value).toBe("2468")
  })

  it("deep-link opens the add dialog with the App pre-picked", async () => {
    stubApi({
      "GET /api/apps": [APP],
      "GET /api/announce-connections": [],
      "GET /api/server-info": { port: 7478 },
    })
    render(wrap(<AnnounceSection initialCreate={{ appId: APP.id }} />))

    // No click: the deep-link itself opens the add dialog…
    const appSelect = await screen.findByLabelText<HTMLSelectElement>("App")
    await screen.findByRole("option", { name: "qui-main-app (qui)" })
    expect(appSelect.value).toBe(String(APP.id))
    // …and the pre-pick (not just the first-free default) seeded the name.
    const nameInput = await screen.findByLabelText<HTMLInputElement>("Name")
    await waitFor(() => expect(nameInput.value).toBe(APP.name))
  })

  it("flipped default: the App picker defaults to the existing app without interaction", async () => {
    stubApi({
      "GET /api/apps": [APP],
      "GET /api/announce-connections": [],
      "GET /api/server-info": { port: 7478 },
    })
    render(wrap(<AnnounceSection />))

    fireEvent.click(await screen.findByRole("button", { name: "Add target" }))
    const appSelect = await screen.findByLabelText<HTMLSelectElement>("App")
    await screen.findByRole("option", { name: "qui-main-app (qui)" })
    expect(appSelect.value).toBe(String(APP.id))
    expect(screen.queryByLabelText("Host")).toBeNull()
  })
})

describe("AnnounceSection create — Already configured block", () => {
  const APP: App = {
    id: 7, kind: "qui", name: "qui-main-app", baseUrl: "http://qui:7476", username: "",
    apiKey: "<redacted>", harbrrUrl: "http://harbrr:7478", enabled: true,
    references: { appConnections: 0, announce: 0, download: 0 },
    createdAt: "2026-07-01T00:00:00Z", updatedAt: "2026-07-01T00:00:00Z",
  }

  function stubFetchWithApps(apps: App[]) {
    stubApi({
      "GET /api/apps": apps,
      "GET /api/announce-connections": [],
      "GET /api/server-info": { port: 7478 },
    })
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
    // A used app would 409 on create (one announce row per App), so it is never the
    // default and its option is disabled, marked "already added".
    const option = await screen.findByRole<HTMLOptionElement>("option", { name: /qui-main-app \(qui\) — already added/ })
    expect(option.disabled).toBe(true)
    expect(appSelect.value).toBe("new")
    expect(screen.getByLabelText("Host")).toBeTruthy()
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

describe("AnnounceSection list states", () => {
  it("a failed targets query renders LoadError, not an empty card", async () => {
    stubApi({
      "GET /api/announce-connections": () => Response.json({ error: "boom" }, { status: 500 }),
      "GET /api/server-info": { port: 7478 },
    })
    render(wrap(<AnnounceSection />))

    const alert = await screen.findByRole("alert")
    expect(alert.textContent).toContain("Loading announce targets failed")
    expect(screen.queryByText(/No announce targets/)).toBeNull()
  })
})

// Two buttons share the "Add target" label once the dialog is open: the toolbar
// opener and the form's own submit button. Disambiguate by element type.
function submitButton(): HTMLButtonElement {
  return screen
    .getAllByRole("button", { name: "Add target" })
    .find((b): b is HTMLButtonElement => b instanceof HTMLButtonElement && b.type === "submit")!
}
