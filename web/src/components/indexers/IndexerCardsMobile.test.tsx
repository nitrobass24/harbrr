import { fireEvent, render, screen, within } from "@testing-library/react"
import { describe, expect, it, vi } from "vitest"
import { IndexerCardsMobile } from "./IndexerCardsMobile"
import type { IndexerRowActions, IndexerRowData } from "./IndexersTable"

const BASE = {
  proxyId: null, solverId: null, protocol: "torrent" as const, freeleech: false, priority: 25, minSeeders: 0,
  syncCategories: [], enableRss: true, enableAutomaticSearch: true, enableInteractiveSearch: true,
  expiresAt: "", expiryKind: "" as const, expiryLifetime: false, failoverDisabled: false,
  createdAt: "2026-07-01T00:00:00Z", updatedAt: "2026-07-01T00:00:00Z",
}

const ROWS: IndexerRowData[] = [
  {
    instance: { id: 1, slug: "torrentleech", definitionId: "torrentleech", name: "TorrentLeech", baseUrl: "https://www.torrentleech.org/", enabled: true, ...BASE },
    type: "private",
    categories: "Movies, TV, Apps",
    status: { slug: "torrentleech", status: "healthy", events: [] },
  },
  {
    instance: { id: 3, slug: "x1337", definitionId: "1337x", name: "1337x", enabled: false, ...BASE },
    type: "public",
    categories: "Movies, TV, Games",
    // status still loading for this row
  },
]

// Usage fixtures (#487): a busy indexer, one that has gone quiet, and one nothing
// has ever queried.
const FAILURES = { authFailure: 0, rateLimited: 0, parseError: 0, antiBot: 0, transport: 0 }
function stat(slug: string, queries: number, lastQueryAt?: string) {
  return { slug, queries, grabAttempts: 0, grabs: 0, avgResponseMs: 120, failures: FAILURES, categories: [], lastQueryAt }
}
const DAY = 24 * 60 * 60 * 1000

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

describe("IndexerCardsMobile", () => {
  it("renders a card per row with name, host, type pill, categories, and health", () => {
    render(<IndexerCardsMobile rows={ROWS} actions={noopActions()} />)

    const tl = screen.getByText("TorrentLeech").closest<HTMLElement>("[data-slug]")!
    expect(within(tl).getByText("www.torrentleech.org")).toBeTruthy()
    expect(within(tl).getByText("Private")).toBeTruthy()
    expect(within(tl).getByText("Movies, TV, Apps")).toBeTruthy()
    expect(within(tl).getByText("Healthy")).toBeTruthy()

    const x = screen.getAllByText("1337x")[0].closest<HTMLElement>("[data-slug]")!
    expect(within(x).getByText("…")).toBeTruthy()
  })

  it("reflects enabled state on the switch and fires the toggle", () => {
    const onToggle = vi.fn()
    render(<IndexerCardsMobile rows={ROWS} actions={noopActions({ onToggle })} />)

    const enabled = screen.getByLabelText("Disable TorrentLeech")
    expect(enabled.getAttribute("data-state")).toBe("checked")
    const disabled = screen.getByLabelText("Enable 1337x")
    expect(disabled.getAttribute("data-state")).toBe("unchecked")

    fireEvent.click(disabled)
    expect(onToggle).toHaveBeenCalledWith("x1337", true)
  })

  it("fires a row action (delete) from the card's actions menu", () => {
    const onDelete = vi.fn()
    render(<IndexerCardsMobile rows={ROWS} actions={noopActions({ onDelete })} />)

    fireEvent.pointerDown(screen.getByLabelText("More actions for TorrentLeech"))
    fireEvent.click(screen.getByText("Delete"))
    expect(onDelete).toHaveBeenCalledWith("torrentleech")
  })

  it("shows usage on the card, with never-queried called out (autobrr/harbrr#487)", () => {
    const rows: IndexerRowData[] = [
      { instance: ROWS[0].instance, stats: stat("torrentleech", 42, new Date(Date.now() - 3 * DAY).toISOString()) },
      { instance: ROWS[1].instance, stats: stat("x1337", 0) },
    ]
    render(<IndexerCardsMobile rows={rows} actions={noopActions()} />)

    const tl = screen.getByText("TorrentLeech").closest<HTMLElement>("[data-slug]")!
    expect(within(tl).getByText("42 queries")).toBeTruthy()
    expect(within(tl).getByText("3d ago")).toBeTruthy()

    const x = screen.getAllByText("1337x")[0].closest<HTMLElement>("[data-slug]")!
    expect(within(x).getByText("Never queried")).toBeTruthy()
  })
})
