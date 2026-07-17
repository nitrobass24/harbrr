import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { fireEvent, render, screen } from "@testing-library/react"
import type { ReactElement } from "react"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"
import { REDACTED } from "@/lib/api"
import type { DefinitionDetail, InstanceDetail, Proxy, Solver } from "@/lib/api"
import { IndexerForm, type IndexerFormSubmit } from "./IndexerForm"

// The form fetches the global proxy/solver resources for its Advanced dropdowns.
const PROXIES: Proxy[] = [{ id: 7, name: "home", type: "socks5", host: "10.0.0.9", port: 1080, username: "", createdAt: "", updatedAt: "" }]
const SOLVERS: Solver[] = [{ id: 9, name: "fs", type: "flaresolverr", url: REDACTED, maxTimeout: 0, createdAt: "", updatedAt: "" }]

function json(body: unknown): Response {
  return new Response(JSON.stringify(body), { status: 200, headers: { "Content-Type": "application/json" } })
}

beforeEach(() => {
  vi.stubGlobal("fetch", vi.fn((request: Request) => {
    if (request.url.endsWith("/api/proxies")) return Promise.resolve(json(PROXIES))
    if (request.url.endsWith("/api/solvers")) return Promise.resolve(json(SOLVERS))
    return Promise.resolve(json([]))
  }))
})
afterEach(() => vi.unstubAllGlobals())

function renderForm(ui: ReactElement) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(<QueryClientProvider client={qc}>{ui}</QueryClientProvider>)
}

const DEFINITION: DefinitionDetail = {
  id: "testtracker",
  name: "Test Tracker",
  type: "private",
  settings: [
    { name: "username", label: "Username", type: "text", secret: false },
    { name: "apikey", label: "API Key", type: "password", secret: true },
  ],
  caps: {
    modes: { search: ["q"] },
    allowRawSearch: false,
    allowTVSearchIMDB: false,
    categories: [],
    limits: { default: 100, max: 100 },
    upstreamLimits: { default: 100, max: 100 },
  },
}

const EXISTING: InstanceDetail = {
  id: 1,
  slug: "tt",
  definitionId: "testtracker",
  name: "TT",
  enabled: true,
  protocol: "torrent",
  proxyId: null,
  solverId: null,
  freeleech: false,
  createdAt: "2026-07-01T00:00:00Z",
  updatedAt: "2026-07-01T00:00:00Z",
  settings: [
    { name: "username", value: "alice", secret: false },
    { name: "apikey", value: REDACTED, secret: true },
  ],
}

describe("IndexerForm", () => {
  it("edit: PATCH payload preserves the sentinel for an untouched secret", () => {
    const onSubmit = vi.fn<(s: IndexerFormSubmit) => void>()
    renderForm(<IndexerForm definition={DEFINITION} existing={EXISTING} pending={false} error={null} onSubmit={onSubmit} />)

    // The secret arrives prefilled with the sentinel in a masked input.
    const secret = screen.getByLabelText("API Key")
    expect((secret as HTMLInputElement).value).toBe(REDACTED)
    expect(secret.getAttribute("type")).toBe("password")

    // Touch a non-secret field only, then save.
    fireEvent.change(screen.getByLabelText("Username"), { target: { value: "bob" } })
    fireEvent.click(screen.getByRole("button", { name: "Save changes" }))

    const submit = onSubmit.mock.calls[0][0]
    expect(submit.mode).toBe("edit")
    expect(submit.body.settings?.apikey).toBe(REDACTED)
    expect(submit.body.settings?.username).toBe("bob")
  })

  it("edit: a rotated secret submits the new plaintext", () => {
    const onSubmit = vi.fn<(s: IndexerFormSubmit) => void>()
    renderForm(<IndexerForm definition={DEFINITION} existing={EXISTING} pending={false} error={null} onSubmit={onSubmit} />)

    fireEvent.change(screen.getByLabelText("API Key"), { target: { value: "fresh-key" } })
    fireEvent.click(screen.getByRole("button", { name: "Save changes" }))

    expect(onSubmit.mock.calls[0][0].body.settings?.apikey).toBe("fresh-key")
  })

  it("create: empty fields are stripped and the definition seeds slug + name", () => {
    const onSubmit = vi.fn<(s: IndexerFormSubmit) => void>()
    renderForm(<IndexerForm definition={DEFINITION} pending={false} error={null} onSubmit={onSubmit} />)

    fireEvent.change(screen.getByLabelText("API Key"), { target: { value: "k123" } })
    fireEvent.click(screen.getByRole("button", { name: "Add indexer" }))

    const submit = onSubmit.mock.calls[0][0]
    expect(submit.mode).toBe("create")
    if (submit.mode === "create") {
      expect(submit.body.definitionId).toBe("testtracker")
      expect(submit.body.slug).toBe("testtracker")
      expect(submit.body.settings).toEqual({ apikey: "k123" })
    }
  })

  it("slug is locked in edit mode", () => {
    renderForm(<IndexerForm definition={DEFINITION} existing={EXISTING} pending={false} error={null} onSubmit={vi.fn()} />)
    expect(screen.getByLabelText<HTMLInputElement>("Slug").disabled).toBe(true)
  })

  it("edit: proxy/solver references prefill the dropdowns and survive a save", async () => {
    const onSubmit = vi.fn<(s: IndexerFormSubmit) => void>()
    renderForm(<IndexerForm definition={DEFINITION} existing={{ ...EXISTING, proxyId: 7, solverId: 9 }} pending={false} error={null} onSubmit={onSubmit} />)

    fireEvent.click(screen.getByRole("button", { name: /Advanced/ }))
    // The dropdown options come from the proxies/solvers queries.
    await screen.findByRole("option", { name: "home (socks5)" })
    await screen.findByRole("option", { name: "fs (FlareSolverr)" })
    expect(screen.getByLabelText<HTMLSelectElement>("Proxy").value).toBe("7")
    expect(screen.getByLabelText<HTMLSelectElement>("Anti-bot solver").value).toBe("9")

    fireEvent.click(screen.getByRole("button", { name: "Save changes" }))
    const body = onSubmit.mock.calls[0][0].body
    expect(body.proxyId).toBe(7)
    expect(body.solverId).toBe(9)
  })

  it("edit: a manual-cookie solver keeps solver_type + the cookie sentinel, no solverId", () => {
    const onSubmit = vi.fn<(s: IndexerFormSubmit) => void>()
    const existing: InstanceDetail = {
      ...EXISTING,
      settings: [
        ...EXISTING.settings,
        { name: "solver_type", value: "manual_cookie", secret: false },
        { name: "cookie", value: REDACTED, secret: true },
      ],
    }
    renderForm(<IndexerForm definition={DEFINITION} existing={existing} pending={false} error={null} onSubmit={onSubmit} />)

    fireEvent.click(screen.getByRole("button", { name: /Advanced/ }))
    expect(screen.getByLabelText<HTMLSelectElement>("Anti-bot solver").value).toBe("cookie")

    fireEvent.click(screen.getByRole("button", { name: "Save changes" }))
    const body = onSubmit.mock.calls[0][0].body
    expect(body.solverId).toBe(null)
    expect(body.settings?.solver_type).toBe("manual_cookie")
    expect(body.settings?.cookie).toBe(REDACTED)
  })

  it("edit: switching the solver from manual-cookie to None clears solver_type + cookie", () => {
    const onSubmit = vi.fn<(s: IndexerFormSubmit) => void>()
    const existing: InstanceDetail = {
      ...EXISTING,
      settings: [
        ...EXISTING.settings,
        { name: "solver_type", value: "manual_cookie", secret: false },
        { name: "cookie", value: REDACTED, secret: true },
      ],
    }
    renderForm(<IndexerForm definition={DEFINITION} existing={existing} pending={false} error={null} onSubmit={onSubmit} />)

    fireEvent.click(screen.getByRole("button", { name: /Advanced/ }))
    // Turn the solver off.
    fireEvent.change(screen.getByLabelText("Anti-bot solver"), { target: { value: "none" } })
    fireEvent.click(screen.getByRole("button", { name: "Save changes" }))

    const body = onSubmit.mock.calls[0][0].body
    expect(body.solverId).toBe(null)
    // Explicit "" so mergeSettings actually removes the stale manual cookie
    // (omitting them would keep the stored values).
    expect(body.settings?.solver_type).toBe("")
    expect(body.settings?.cookie).toBe("")
  })

  it("edit: a definition's own cookie field renders normally and is preserved untouched", () => {
    const onSubmit = vi.fn<(s: IndexerFormSubmit) => void>()
    const cookieDef: DefinitionDetail = {
      ...DEFINITION,
      settings: [
        { name: "cookie", label: "Cookie", type: "password", secret: true },
      ],
    }
    const existing: InstanceDetail = {
      ...EXISTING,
      settings: [{ name: "cookie", value: REDACTED, secret: true }],
    }
    renderForm(<IndexerForm definition={cookieDef} existing={existing} pending={false} error={null} onSubmit={onSubmit} />)

    // The def's Cookie field renders as a normal masked credential field (prefilled
    // with the sentinel), NOT blanked by the solver-managed-keys stripping.
    const cookieField = screen.getByLabelText<HTMLInputElement>("Cookie")
    expect(cookieField.value).toBe(REDACTED)
    expect(cookieField.getAttribute("type")).toBe("password")

    fireEvent.click(screen.getByRole("button", { name: "Save changes" }))
    // Untouched -> the sentinel rides back and the server keeps the stored cookie.
    expect(onSubmit.mock.calls[0][0].body.settings?.cookie).toBe(REDACTED)
  })
})
