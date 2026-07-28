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
  links: ["https://tt.example", "https://mirror.tt.example"],
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
  priority: 30,
  minSeeders: 4,
  syncCategories: [],
  enableRss: true,
  enableAutomaticSearch: true,
  enableInteractiveSearch: true,
  expiresAt: "",
  expiryKind: "" as const,
  expiryLifetime: false,
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
      // The matching settings ride along with their explicit defaults (both = today's
      // engine behaviour); every other untouched field is stripped.
      expect(submit.body.settings).toEqual({
        apikey: "k123", andmatch_fold_punctuation: "false", degenerate_query_gate: "off",
      })
      expect(submit.body.priority).toBe(25)
      expect(submit.body.minSeeders).toBe(0)
    }
  })

  it("edit: priority/minSeeders prefill from the instance and submit as numbers", () => {
    const onSubmit = vi.fn<(s: IndexerFormSubmit) => void>()
    renderForm(<IndexerForm definition={DEFINITION} existing={EXISTING} pending={false} error={null} onSubmit={onSubmit} />)

    fireEvent.click(screen.getByRole("button", { name: /Advanced/ }))
    expect(screen.getByLabelText<HTMLInputElement>(/Priority/).value).toBe("30")
    expect(screen.getByLabelText<HTMLInputElement>(/Minimum seeders/).value).toBe("4")

    fireEvent.change(screen.getByLabelText(/Priority/), { target: { value: "12" } })
    fireEvent.click(screen.getByRole("button", { name: "Save changes" }))

    const body = onSubmit.mock.calls[0][0].body
    expect(body.priority).toBe(12)
    expect(body.minSeeders).toBe(4)
  })

  it("edit: request-limit settings (query_limit/grab_limit/limits_unit) round-trip", () => {
    const onSubmit = vi.fn<(s: IndexerFormSubmit) => void>()
    const existing: InstanceDetail = {
      ...EXISTING,
      settings: [
        ...EXISTING.settings,
        { name: "query_limit", value: "100", secret: false },
        { name: "grab_limit", value: "20", secret: false },
        { name: "limits_unit", value: "hour", secret: false },
      ],
    }
    renderForm(<IndexerForm definition={DEFINITION} existing={existing} pending={false} error={null} onSubmit={onSubmit} />)

    fireEvent.click(screen.getByRole("button", { name: /Advanced/ }))
    expect(screen.getByLabelText<HTMLInputElement>(/Search request cap/).value).toBe("100")
    expect(screen.getByLabelText<HTMLInputElement>(/Grab request cap/).value).toBe("20")
    expect(screen.getByLabelText<HTMLSelectElement>(/Limits reset every/).value).toBe("hour")

    fireEvent.click(screen.getByRole("button", { name: "Save changes" }))
    const body = onSubmit.mock.calls[0][0].body
    expect(body.settings?.query_limit).toBe("100")
    expect(body.settings?.grab_limit).toBe("20")
    expect(body.settings?.limits_unit).toBe("hour")
  })

  // autobrr/harbrr#394: both matching settings are reserved per-instance keys, so the
  // form is the only place they can be set. Default off on create; stored values
  // prefill and round-trip on edit.
  it("create: the matching settings default to today's behaviour", () => {
    const onSubmit = vi.fn<(s: IndexerFormSubmit) => void>()
    renderForm(<IndexerForm definition={DEFINITION} pending={false} error={null} onSubmit={onSubmit} />)

    fireEvent.click(screen.getByRole("button", { name: /Advanced/ }))
    expect(screen.getByLabelText(/Ignore apostrophes/).getAttribute("data-state")).toBe("unchecked")
    expect(screen.getByLabelText<HTMLSelectElement>(/Skip searches/).value).toBe("off")

    fireEvent.click(screen.getByRole("button", { name: "Add indexer" }))
    const body = onSubmit.mock.calls[0][0].body
    expect(body.settings?.andmatch_fold_punctuation).toBe("false")
    expect(body.settings?.degenerate_query_gate).toBe("off")
  })

  it("edit: matching settings prefill from the instance and submit on save", () => {
    const onSubmit = vi.fn<(s: IndexerFormSubmit) => void>()
    const existing: InstanceDetail = {
      ...EXISTING,
      settings: [
        ...EXISTING.settings,
        { name: "andmatch_fold_punctuation", value: "true", secret: false },
        { name: "degenerate_query_gate", value: "auto", secret: false },
      ],
    }
    renderForm(<IndexerForm definition={DEFINITION} existing={existing} pending={false} error={null} onSubmit={onSubmit} />)

    fireEvent.click(screen.getByRole("button", { name: /Advanced/ }))
    expect(screen.getByLabelText(/Ignore apostrophes/).getAttribute("data-state")).toBe("checked")
    expect(screen.getByLabelText<HTMLSelectElement>(/Skip searches/).value).toBe("auto")

    fireEvent.click(screen.getByRole("button", { name: "Save changes" }))
    const body = onSubmit.mock.calls[0][0].body
    expect(body.settings?.andmatch_fold_punctuation).toBe("true")
    expect(body.settings?.degenerate_query_gate).toBe("auto")
  })

  it("edit: sync-categories/toggles prefill from the instance and submit on save", () => {
    const onSubmit = vi.fn<(s: IndexerFormSubmit) => void>()
    const existing: InstanceDetail = { ...EXISTING, syncCategories: [5000, 3030], enableAutomaticSearch: false }
    renderForm(<IndexerForm definition={DEFINITION} existing={existing} pending={false} error={null} onSubmit={onSubmit} />)

    fireEvent.click(screen.getByRole("button", { name: /Advanced/ }))
    expect(screen.getByLabelText("TV")).toHaveProperty("dataset.state", "checked")
    expect(screen.getByPlaceholderText(/Extra category IDs/)).toHaveProperty("value", "3030")
    expect(screen.getByLabelText("Enable automatic search")).toHaveProperty("dataset.state", "unchecked")

    fireEvent.click(screen.getByRole("button", { name: "Save changes" }))
    const body = onSubmit.mock.calls[0][0].body
    expect(body.syncCategories).toEqual([3030, 5000])
    expect(body.enableRss).toBe(true)
    expect(body.enableAutomaticSearch).toBe(false)
    expect(body.enableInteractiveSearch).toBe(true)
  })

  it("create: defaults to every toggle on and no category narrowing", () => {
    const onSubmit = vi.fn<(s: IndexerFormSubmit) => void>()
    renderForm(<IndexerForm definition={DEFINITION} pending={false} error={null} onSubmit={onSubmit} />)

    fireEvent.change(screen.getByLabelText("API Key"), { target: { value: "k123" } })
    fireEvent.click(screen.getByRole("button", { name: "Add indexer" }))

    const body = onSubmit.mock.calls[0][0].body
    expect(body.syncCategories).toEqual([])
    expect(body.enableRss).toBe(true)
    expect(body.enableAutomaticSearch).toBe(true)
    expect(body.enableInteractiveSearch).toBe(true)
  })

  it("create: an untouched expiry group submits as untracked", () => {
    const onSubmit = vi.fn<(s: IndexerFormSubmit) => void>()
    renderForm(<IndexerForm definition={DEFINITION} pending={false} error={null} onSubmit={onSubmit} />)

    fireEvent.change(screen.getByLabelText("API Key"), { target: { value: "k123" } })
    fireEvent.click(screen.getByRole("button", { name: /Advanced/ }))
    expect(screen.getByLabelText<HTMLInputElement>(/^Expiry \(optional\)/).value).toBe("")
    fireEvent.click(screen.getByRole("button", { name: "Add indexer" }))

    const body = onSubmit.mock.calls[0][0].body
    expect(body.expiresAt).toBe("")
    expect(body.expiryLifetime).toBe(false)
  })

  it("create: a date and kind ride the create body", () => {
    const onSubmit = vi.fn<(s: IndexerFormSubmit) => void>()
    renderForm(<IndexerForm definition={DEFINITION} pending={false} error={null} onSubmit={onSubmit} />)

    fireEvent.change(screen.getByLabelText("API Key"), { target: { value: "k123" } })
    fireEvent.click(screen.getByRole("button", { name: /Advanced/ }))
    fireEvent.change(screen.getByLabelText(/^Expiry \(optional\)/), { target: { value: "2027-03-01" } })
    fireEvent.change(screen.getByLabelText("Expiry type"), { target: { value: "account" } })
    fireEvent.click(screen.getByRole("button", { name: "Add indexer" }))

    const body = onSubmit.mock.calls[0][0].body
    expect(body.expiresAt).toBe("2027-03-01")
    expect(body.expiryKind).toBe("account")
  })

  it("edit: the expiry prefills, and clearing the date submits an empty string", () => {
    const onSubmit = vi.fn<(s: IndexerFormSubmit) => void>()
    const existing: InstanceDetail = { ...EXISTING, expiresAt: "2027-03-01", expiryKind: "perk" }
    renderForm(<IndexerForm definition={DEFINITION} existing={existing} pending={false} error={null} onSubmit={onSubmit} />)

    fireEvent.click(screen.getByRole("button", { name: /Advanced/ }))
    const date = screen.getByLabelText<HTMLInputElement>(/^Expiry \(optional\)/)
    expect(date.value).toBe("2027-03-01")
    expect(screen.getByLabelText<HTMLSelectElement>("Expiry type").value).toBe("perk")

    // Clearing must reach the server as "" — an omitted field would leave the stored
    // date in place and keep warning about an expiry the operator just removed.
    fireEvent.change(date, { target: { value: "" } })
    fireEvent.click(screen.getByRole("button", { name: "Save changes" }))
    expect(onSubmit.mock.calls[0][0].body.expiresAt).toBe("")
  })

  it("edit: stored matching settings survive an unrelated edit", () => {
    // Kills the recurring seeded-values false positive for the two engine settings:
    // defaultValues' stored loop applies EVERY stored setting over the reserved-field
    // defaults (settings-payload.ts), so "true"/"auto" must round-trip a name-only edit.
    const onSubmit = vi.fn<(s: IndexerFormSubmit) => void>()
    const existing: InstanceDetail = {
      ...EXISTING,
      settings: [
        ...EXISTING.settings,
        { name: "andmatch_fold_punctuation", value: "true", secret: false },
        { name: "degenerate_query_gate", value: "auto", secret: false },
      ],
    }
    renderForm(<IndexerForm definition={DEFINITION} existing={existing} pending={false} error={null} onSubmit={onSubmit} />)

    fireEvent.change(screen.getByLabelText<HTMLInputElement>("Name"), { target: { value: "Renamed" } })
    fireEvent.click(screen.getByRole("button", { name: "Save changes" }))

    const submit = onSubmit.mock.calls[0][0]
    expect(submit.body.settings?.andmatch_fold_punctuation).toBe("true")
    expect(submit.body.settings?.degenerate_query_gate).toBe("auto")
  })

  it("edit: changing only the name preserves the stored expiry untouched", () => {
    // The preservation class this form keeps getting bitten by: an unrelated edit must
    // not reset or drop operator-entered expiry data on submit.
    const onSubmit = vi.fn<(s: IndexerFormSubmit) => void>()
    const existing: InstanceDetail = { ...EXISTING, expiresAt: "2027-03-01", expiryKind: "account", expiryLifetime: false }
    renderForm(<IndexerForm definition={DEFINITION} existing={existing} pending={false} error={null} onSubmit={onSubmit} />)

    fireEvent.change(screen.getByLabelText<HTMLInputElement>("Name"), { target: { value: "Renamed" } })
    fireEvent.click(screen.getByRole("button", { name: "Save changes" }))

    const body = onSubmit.mock.calls[0][0].body
    expect(body.name).toBe("Renamed")
    expect(body.expiresAt).toBe("2027-03-01")
    expect(body.expiryKind).toBe("account")
    expect(body.expiryLifetime).toBe(false)
  })

  it("edit: ticking Lifetime disables the date field and submits the flag", () => {
    const onSubmit = vi.fn<(s: IndexerFormSubmit) => void>()
    const existing: InstanceDetail = { ...EXISTING, expiresAt: "2027-03-01", expiryKind: "perk" }
    renderForm(<IndexerForm definition={DEFINITION} existing={existing} pending={false} error={null} onSubmit={onSubmit} />)

    fireEvent.click(screen.getByRole("button", { name: /Advanced/ }))
    fireEvent.click(screen.getByLabelText("Lifetime (never expires)"))
    expect(screen.getByLabelText<HTMLInputElement>(/^Expiry \(optional\)/).disabled).toBe(true)

    fireEvent.click(screen.getByRole("button", { name: "Save changes" }))
    expect(onSubmit.mock.calls[0][0].body.expiryLifetime).toBe(true)
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

  // autobrr/harbrr#401 — the base-URL override is picked from the definition's
  // known links, with a mandatory custom escape hatch.
  describe("base URL picker", () => {
    const pick = () => screen.getByLabelText<HTMLSelectElement>("Base URL (optional override)")
    const withBaseUrl = (baseUrl: string): InstanceDetail => ({ ...EXISTING, baseUrl })

    it("offers Default (labelled with the resolved host), every link, and Custom…", () => {
      renderForm(<IndexerForm definition={DEFINITION} pending={false} error={null} onSubmit={vi.fn()} />)
      expect(screen.getAllByRole("option").map((o) => o.textContent)).toEqual([
        "Default (tt.example)", "tt.example", "mirror.tt.example", "Custom…",
      ])
      expect(pick().value).toBe("")
    })

    // Trailing-slash and host-case differences must not force a false Custom.
    it.each([
      ["", ""],
      ["https://mirror.tt.example", "https://mirror.tt.example"],
      ["https://tt.example/", "https://tt.example"],
      ["https://TT.example", "https://tt.example"],
      ["https://private.mirror.lan", "custom"],
    ])("edit: stored %p preselects %p", (stored, want) => {
      renderForm(<IndexerForm definition={DEFINITION} existing={withBaseUrl(stored)} pending={false} error={null} onSubmit={vi.fn()} />)
      expect(pick().value).toBe(want)
    })

    it("edit: an off-list stored value stays intact through an unrelated edit", () => {
      const onSubmit = vi.fn<(s: IndexerFormSubmit) => void>()
      renderForm(<IndexerForm definition={DEFINITION} existing={withBaseUrl("https://private.mirror.lan")} pending={false} error={null} onSubmit={onSubmit} />)

      expect(screen.getByLabelText<HTMLInputElement>("Custom base URL").value).toBe("https://private.mirror.lan")
      fireEvent.change(screen.getByLabelText("Username"), { target: { value: "bob" } })
      fireEvent.click(screen.getByRole("button", { name: "Save changes" }))

      expect(onSubmit.mock.calls[0][0].body.baseUrl).toBe("https://private.mirror.lan")
    })

    it("edit: selecting Default submits \"\" so the stored override is cleared", () => {
      const onSubmit = vi.fn<(s: IndexerFormSubmit) => void>()
      renderForm(<IndexerForm definition={DEFINITION} existing={withBaseUrl("https://mirror.tt.example")} pending={false} error={null} onSubmit={onSubmit} />)

      fireEvent.change(pick(), { target: { value: "" } })
      fireEvent.click(screen.getByRole("button", { name: "Save changes" }))

      expect(onSubmit.mock.calls[0][0].body.baseUrl).toBe("")
    })

    it("edit: picking a known link submits it verbatim", () => {
      const onSubmit = vi.fn<(s: IndexerFormSubmit) => void>()
      renderForm(<IndexerForm definition={DEFINITION} existing={EXISTING} pending={false} error={null} onSubmit={onSubmit} />)

      fireEvent.change(pick(), { target: { value: "https://mirror.tt.example" } })
      fireEvent.click(screen.getByRole("button", { name: "Save changes" }))

      expect(onSubmit.mock.calls[0][0].body.baseUrl).toBe("https://mirror.tt.example")
    })

    it("create: an untouched picker omits baseUrl entirely", () => {
      const onSubmit = vi.fn<(s: IndexerFormSubmit) => void>()
      renderForm(<IndexerForm definition={DEFINITION} pending={false} error={null} onSubmit={onSubmit} />)

      fireEvent.click(screen.getByRole("button", { name: "Add indexer" }))
      const submit = onSubmit.mock.calls[0][0]
      if (submit.mode === "create") expect(submit.body.baseUrl).toBeUndefined()
    })

    it("create: an off-list custom host warns but is accepted", () => {
      const onSubmit = vi.fn<(s: IndexerFormSubmit) => void>()
      renderForm(<IndexerForm definition={DEFINITION} pending={false} error={null} onSubmit={onSubmit} />)

      fireEvent.change(pick(), { target: { value: "custom" } })
      fireEvent.change(screen.getByLabelText("Custom base URL"), { target: { value: "https://private.mirror.lan" } })
      expect(screen.getByText(/known hosts/)).toBeTruthy()

      fireEvent.click(screen.getByRole("button", { name: "Add indexer" }))
      const submit = onSubmit.mock.calls[0][0]
      if (submit.mode === "create") expect(submit.body.baseUrl).toBe("https://private.mirror.lan")
    })

    it("create: a non-http scheme blocks submit", () => {
      renderForm(<IndexerForm definition={DEFINITION} pending={false} error={null} onSubmit={vi.fn()} />)

      fireEvent.change(pick(), { target: { value: "custom" } })
      fireEvent.change(screen.getByLabelText("Custom base URL"), { target: { value: "ftp://nope.example" } })

      expect(screen.getByText(/must start with http/)).toBeTruthy()
      expect(screen.getByRole("button", { name: "Add indexer" })).toHaveProperty("disabled", true)
    })

    // Native drivers carry a single-entry links list; nothing special-cased.
    it("a single-link definition still offers Default + the link + Custom…", () => {
      const native: DefinitionDetail = { ...DEFINITION, links: ["https://avistaz.to/"] }
      renderForm(<IndexerForm definition={native} pending={false} error={null} onSubmit={vi.fn()} />)
      expect(screen.getAllByRole("option").map((o) => o.textContent)).toEqual([
        "Default (avistaz.to)", "avistaz.to", "Custom…",
      ])
    })

    // Shouldn't happen, but a linkless definition must not lose the override control.
    it("a definition with no links falls back to free text", () => {
      const onSubmit = vi.fn<(s: IndexerFormSubmit) => void>()
      renderForm(<IndexerForm definition={{ ...DEFINITION, links: [] }} pending={false} error={null} onSubmit={onSubmit} />)

      fireEvent.change(screen.getByLabelText("Base URL (optional override)"), { target: { value: "https://only.example" } })
      fireEvent.click(screen.getByRole("button", { name: "Add indexer" }))

      const submit = onSubmit.mock.calls[0][0]
      if (submit.mode === "create") expect(submit.body.baseUrl).toBe("https://only.example")
    })
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
