import { fireEvent, render, screen, within } from "@testing-library/react"
import { describe, expect, it, vi } from "vitest"
import { IndexerCardsMobile } from "./IndexerCardsMobile"
import type { IndexerRowActions, IndexerRowData } from "./IndexersTable"

const BASE = { proxyId: null, solverId: null, protocol: "torrent" as const, freeleech: false, priority: 25, minSeeders: 0, createdAt: "2026-07-01T00:00:00Z", updatedAt: "2026-07-01T00:00:00Z" }

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
})
