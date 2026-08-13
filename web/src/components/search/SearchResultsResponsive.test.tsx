import { render, screen } from "@testing-library/react"
import { describe, expect, it, vi } from "vitest"
import * as mediaQuery from "@/hooks/useMediaQuery"
import { groupRows, soloGroups } from "./search-group"
import type { SearchRow, Sort } from "./search-sort"
import { SearchResultsResponsive } from "./SearchResultsResponsive"

const ROWS: SearchRow[] = [
  {
    indexer: "demotracker",
    release: {
      title: "Big Buck Bunny 1080p",
      link: "http://tracker.example/dl?id=1&passkey=NOTREAL",
      size: 2_684_354_560,
      categories: [2000],
      seeders: 42,
      leechers: 7,
    },
  },
]

// The same release on two trackers, matched on a shared infohash.
const GROUPED: SearchRow[] = [
  { indexer: "demotracker", protocol: "torrent", release: { title: "Sintel 1080p", infohash: "abc", size: 1, seeders: 1 } },
  { indexer: "demopublic", protocol: "torrent", release: { title: "sintel.1080p", infohash: "ABC", size: 1, seeders: 2 } },
]

const CATS = new Map([[2000, "Movies"]])
const SORT: Sort = { key: "seeders", dir: "desc" }

function renderResponsive(groups = soloGroups(ROWS)) {
  const onSort = vi.fn()
  render(<SearchResultsResponsive groups={groups} catNames={CATS} sort={SORT} onSort={onSort} />)
  return onSort
}

describe("SearchResultsResponsive", () => {
  it("renders cards on mobile viewports", () => {
    vi.spyOn(mediaQuery, "useIsMobile").mockReturnValue(true)
    renderResponsive()

    // Card layout has no <table>; the sortable column headers are table-only.
    expect(screen.queryByRole("table")).toBeNull()
    expect(screen.getByText("Big Buck Bunny 1080p")).toBeTruthy()

    vi.restoreAllMocks()
  })

  it("renders the table on md+ viewports", () => {
    vi.spyOn(mediaQuery, "useIsMobile").mockReturnValue(false)
    renderResponsive()

    expect(screen.getByRole("table")).toBeTruthy()
    expect(screen.getByText("Big Buck Bunny 1080p")).toBeTruthy()

    vi.restoreAllMocks()
  })

  it("fires the Grab action from a mobile card via the same href as the table", () => {
    vi.spyOn(mediaQuery, "useIsMobile").mockReturnValue(true)
    renderResponsive()

    const grab = screen.getByLabelText("Download Big Buck Bunny 1080p")
    expect(grab.getAttribute("href")).toBe("http://tracker.example/dl?id=1&passkey=NOTREAL")

    vi.restoreAllMocks()
  })

  it.each([true, false])("shows the grouped release with both sources on mobile=%s", (mobile) => {
    vi.spyOn(mediaQuery, "useIsMobile").mockReturnValue(mobile)
    renderResponsive(groupRows(GROUPED))

    expect(screen.getByText("demotracker")).toBeTruthy()
    expect(screen.getByText("demopublic")).toBeTruthy()
    expect(screen.getByRole("button", { name: /Expand .* 2 sources/ })).toBeTruthy()

    vi.restoreAllMocks()
  })
})
