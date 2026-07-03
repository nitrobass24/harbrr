import { fireEvent, render, screen, within } from "@testing-library/react"
import { describe, expect, it, vi } from "vitest"
import type { IndexerRowActions, IndexerRowData } from "./IndexersTable"
import { IndexersTable } from "./IndexersTable"

const BASE = { createdAt: "2026-07-01T00:00:00Z", updatedAt: "2026-07-01T00:00:00Z" }

const ROWS: IndexerRowData[] = [
  {
    instance: { slug: "torrentleech", definitionId: "torrentleech", name: "TorrentLeech", baseUrl: "https://www.torrentleech.org/", enabled: true, ...BASE },
    type: "private",
    categories: "Movies, TV, Apps",
    status: { slug: "torrentleech", status: "healthy", events: [] },
  },
  {
    instance: { slug: "rutor", definitionId: "rutor", name: "rutor", baseUrl: "https://rutor.info/", enabled: true, ...BASE },
    type: "public",
    categories: "Movies, TV",
    status: {
      slug: "rutor",
      status: "unhealthy",
      events: [{ kind: "auth_failure", detail: "login failed", occurred_at: new Date(Date.now() - 120_000).toISOString() }],
    },
  },
  {
    instance: { slug: "x1337", definitionId: "1337x", name: "1337x", enabled: false, ...BASE },
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

describe("IndexersTable", () => {
  it("renders name, host, type pill, categories, and health per row", () => {
    render(<IndexersTable rows={ROWS} actions={noopActions()} />)

    // Healthy private row.
    const tl = screen.getByText("TorrentLeech").closest("tr")!
    expect(within(tl).getByText("www.torrentleech.org")).toBeTruthy()
    expect(within(tl).getByText("Private")).toBeTruthy()
    expect(within(tl).getByText("Movies, TV, Apps")).toBeTruthy()
    expect(within(tl).getByText("Healthy")).toBeTruthy()

    // Unhealthy row surfaces the failure kind + relative time.
    const ru = screen.getByText("rutor").closest("tr")!
    expect(within(ru).getByText("Error")).toBeTruthy()
    expect(within(ru).getByText(/auth failed 2m ago/)).toBeTruthy()

    // Row with the status probe still in flight shows the pending marker
    // ("1337x" appears as both name and host fallback, hence getAllByText).
    const x = screen.getAllByText("1337x")[0].closest("tr")!
    expect(within(x).getByText("…")).toBeTruthy()
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
