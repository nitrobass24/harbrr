import { render, screen, within } from "@testing-library/react"
import { describe, expect, it, vi } from "vitest"
import type { SearchRow } from "./search-sort"
import { sortRows } from "./search-sort"
import { SearchResultsTable } from "./SearchResultsTable"

const NOW = Date.now()

const ROWS: SearchRow[] = [
  {
    indexer: "demotracker",
    release: {
      title: "Big Buck Bunny 1080p",
      link: "http://tracker.example/dl?id=1&passkey=NOTREAL",
      size: 2_684_354_560, // 2.5 GiB
      categories: [2000],
      seeders: 42,
      leechers: 7,
      publishDate: new Date(NOW - 2 * 3600_000).toISOString(),
      downloadVolumeFactor: 1,
    },
  },
  {
    indexer: "demopublic",
    release: {
      title: "Sintel 720p FL",
      magnet: "magnet:?xt=urn:btih:abc",
      size: 734_003_200, // 700 MiB
      categories: [5000],
      seeders: 0,
      leechers: 1,
      publishDate: new Date(NOW - 3 * 86_400_000).toISOString(),
      downloadVolumeFactor: 0, // freeleech
    },
  },
]

const CATS = new Map([[2000, "Movies"], [5000, "TV"]])

function renderTable(rows = ROWS) {
  const onSort = vi.fn()
  render(<SearchResultsTable rows={rows} catNames={CATS} sort={{ key: "seeders", dir: "desc" }} onSort={onSort} />)
  return onSort
}

describe("SearchResultsTable", () => {
  it("formats size, age, seeders, and category names", () => {
    renderTable()

    const bunny = screen.getByText("Big Buck Bunny 1080p").closest("tr")!
    expect(within(bunny).getByText("2.5 GiB")).toBeTruthy()
    expect(within(bunny).getByText("2h ago")).toBeTruthy()
    expect(within(bunny).getByText("42")).toBeTruthy()
    expect(within(bunny).getByText("Movies")).toBeTruthy()
    expect(within(bunny).getByText("demotracker")).toBeTruthy()

    const sintel = screen.getByText("Sintel 720p FL").closest("tr")!
    expect(within(sintel).getByText("700.0 MiB")).toBeTruthy()
    expect(within(sintel).getByText("3d ago")).toBeTruthy()
  })

  it("renders the grab link href verbatim (never rebuilt)", () => {
    renderTable()
    const grab = screen.getByLabelText("Download Big Buck Bunny 1080p")
    expect(grab.getAttribute("href")).toBe("http://tracker.example/dl?id=1&passkey=NOTREAL")
    const magnet = screen.getByLabelText("Magnet for Sintel 720p FL")
    expect(magnet.getAttribute("href")).toBe("magnet:?xt=urn:btih:abc")
  })

  it("marks freeleech releases with the FL badge", () => {
    renderTable()
    const sintel = screen.getByText("Sintel 720p FL").closest("tr")!
    expect(within(sintel).getByText("FL")).toBeTruthy()
    const bunny = screen.getByText("Big Buck Bunny 1080p").closest("tr")!
    expect(within(bunny).queryByText("FL")).toBeNull()
  })
})

describe("sortRows", () => {
  it("sorts by seeders desc by default ordering semantics", () => {
    const sorted = sortRows(ROWS, { key: "seeders", dir: "desc" })
    expect(sorted[0].release.title).toBe("Big Buck Bunny 1080p")
  })

  it("sorts by size asc", () => {
    const sorted = sortRows(ROWS, { key: "size", dir: "asc" })
    expect(sorted[0].release.title).toBe("Sintel 720p FL")
  })

  it("age desc puts the oldest release first", () => {
    const sorted = sortRows(ROWS, { key: "age", dir: "desc" })
    expect(sorted[0].release.title).toBe("Sintel 720p FL")
  })
})
