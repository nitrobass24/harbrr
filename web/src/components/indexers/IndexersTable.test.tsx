import { fireEvent, render, screen, within } from "@testing-library/react"
import { describe, expect, it, vi } from "vitest"
import type { IndexerRowActions, IndexerRowData } from "./IndexersTable"
import { IndexersTable } from "./IndexersTable"

const BASE = {
  proxyId: null, solverId: null, protocol: "torrent" as const, freeleech: false, priority: 25, minSeeders: 0,
  syncCategories: [], enableRss: true, enableAutomaticSearch: true, enableInteractiveSearch: true,
  expiresAt: "", expiryKind: "" as const, expiryLifetime: false,
  createdAt: "2026-07-01T00:00:00Z", updatedAt: "2026-07-01T00:00:00Z",
}

const ROWS: IndexerRowData[] = [
  {
    instance: { id: 1, slug: "torrentleech", definitionId: "torrentleech", name: "TorrentLeech", baseUrl: "https://www.torrentleech.org/", enabled: true, ...BASE },
    type: "private",
    categories: "Movies, TV, Apps",
    status: {
      slug: "torrentleech",
      status: "healthy",
      events: [{ kind: "parse_error", detail: "old failure", occurred_at: new Date(Date.now() - 3_600_000).toISOString() }],
    },
  },
  {
    instance: { id: 2, slug: "rutor", definitionId: "rutor", name: "rutor", baseUrl: "https://rutor.info/", enabled: true, ...BASE, freeleech: true },
    type: "public",
    categories: "Movies, TV",
    status: {
      slug: "rutor",
      status: "failing",
      events: [{ kind: "auth_failure", detail: "login failed", occurred_at: new Date(Date.now() - 120_000).toISOString() }],
    },
  },
  {
    instance: { id: 3, slug: "x1337", definitionId: "1337x", name: "1337x", enabled: false, ...BASE },
    type: "public",
    categories: "Movies, TV, Games",
    // status still loading for this row
  },
  {
    instance: { id: 4, slug: "nyaa", definitionId: "nyaasi", name: "Nyaa", baseUrl: "https://nyaa.si/", enabled: true, ...BASE },
    type: "public",
    categories: "Anime",
    // Nothing recent is known: the old failure has aged out and nothing has
    // succeeded since (autobrr/harbrr#389).
    status: {
      slug: "nyaa",
      status: "unknown",
      events: [{ kind: "transport", detail: "connection refused", occurred_at: new Date(Date.now() - 7_200_000).toISOString() }],
    },
  },
]

function noopActions(overrides: Partial<IndexerRowActions> = {}): IndexerRowActions {
  return {
    onToggle: vi.fn(),
    onTest: vi.fn(),
    onEdit: vi.fn(),
    onDelete: vi.fn(),
    onSnippet: vi.fn(),
    onCopyFeedUrl: vi.fn(),
    onDetails: vi.fn(),
    ...overrides,
  }
}

describe("IndexersTable", () => {
  it("renders name, host, type pill, categories, and health per row", () => {
    render(<IndexersTable rows={ROWS} actions={noopActions()} />)

    // Healthy private row.
    const tl = screen.getByText("TorrentLeech").closest("tr")!
    expect(within(tl).getByText("www.torrentleech.org")).toBeTruthy()
    expect(within(tl).getByText("Private")).toBeTruthy()
    expect(within(tl).getByText("Movies, TV, Apps")).toBeTruthy()
    expect(within(tl).getByText("Healthy")).toBeTruthy()
    expect(within(tl).queryByText(/parse error/)).toBeNull()

    // Failing row surfaces the failure kind + relative time, in operator language
    // rather than the raw kind constant (autobrr/harbrr#389).
    const ru = screen.getByText("rutor").closest("tr")!
    expect(within(ru).getByText("Failing")).toBeTruthy()
    expect(within(ru).getByText(/login failed 2m ago/)).toBeTruthy()

    // A never-tested row reads "Never tested" with NO event caption (autobrr/harbrr#473):
    // the state asserts nothing, so captioning an old failure would read as current. The
    // dated history stays in the expanded detail.
    const ny = screen.getByText("Nyaa").closest("tr")!
    expect(within(ny).getByText("Never tested")).toBeTruthy()
    expect(within(ny).queryByText(/couldn't reach the tracker/)).toBeNull()

    // Row with the status probe still in flight shows the pending marker
    // ("1337x" appears as both name and host fallback, hence getAllByText).
    const x = screen.getAllByText("1337x")[0].closest("tr")!
    expect(within(x).getByText("…")).toBeTruthy()
  })

  it("shows the Freeleech badge only for instances with freeleech enabled", () => {
    render(<IndexersTable rows={ROWS} actions={noopActions()} />)

    const ru = screen.getByText("rutor").closest("tr")!
    expect(within(ru).getByText("Freeleech")).toBeTruthy()

    const tl = screen.getByText("TorrentLeech").closest("tr")!
    expect(within(tl).queryByText("Freeleech")).toBeNull()
  })

  it("reflects enabled state on the switch and fires the toggle", () => {
    const onToggle = vi.fn()
    render(<IndexersTable rows={ROWS} actions={noopActions({ onToggle })} />)

    const enabled = screen.getByLabelText("Disable TorrentLeech")
    expect(enabled.getAttribute("data-state")).toBe("checked")
    const disabled = screen.getByLabelText("Enable 1337x")
    expect(disabled.getAttribute("data-state")).toBe("unchecked")

    fireEvent.click(disabled)
    expect(onToggle).toHaveBeenCalledWith("x1337", true)
  })

  it("shows an expiry per row, quiet when untracked and loud once expired", () => {
    const rows: IndexerRowData[] = [
      { instance: { ...ROWS[0].instance, expiresAt: "2020-01-01", expiryKind: "perk" } },
      { instance: { ...ROWS[1].instance } },
      { instance: { ...ROWS[2].instance, expiryLifetime: true } },
    ]
    render(<IndexersTable rows={rows} actions={noopActions()} />)

    const row = (slug: string) => document.querySelector<HTMLElement>(`tr[data-slug="${slug}"]`)!
    expect(within(row("torrentleech")).getByText("Expired")).toBeTruthy()
    expect(within(row("rutor")).getByText("—")).toBeTruthy()
    expect(within(row("x1337")).getByText("Lifetime")).toBeTruthy()
  })

  it("exposes the expiry sort as a pressable header", () => {
    const onToggleExpirySort = vi.fn()
    render(<IndexersTable rows={ROWS} actions={noopActions()} onToggleExpirySort={onToggleExpirySort} />)

    const header = screen.getByRole("button", { name: /Expiry/ })
    expect(header.getAttribute("aria-pressed")).toBe("false")
    fireEvent.click(header)
    expect(onToggleExpirySort).toHaveBeenCalled()
  })

  it("fires the test action and shows the in-flight state", () => {
    const onTest = vi.fn()
    const testing = ROWS.map((r, i) => (i === 0 ? { ...r, testing: true } : r))
    render(<IndexersTable rows={testing} actions={noopActions({ onTest })} />)

    expect(screen.getByText("Testing…")).toBeTruthy()
    const ru = screen.getByText("rutor").closest("tr")!
    fireEvent.click(within(ru).getByRole("button", { name: /Test/ }))
    expect(onTest).toHaveBeenCalledWith("rutor")
  })
})
