import { render, screen } from "@testing-library/react"
import { describe, expect, it, vi } from "vitest"
import * as mediaQuery from "@/hooks/useMediaQuery"
import type { SearchRow } from "./search-sort"
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

const CATS = new Map([[2000, "Movies"]])

function renderResponsive() {
  const onSort = vi.fn()
  render(<SearchResultsResponsive rows={ROWS} catNames={CATS} sort={{ key: "seeders", dir: "desc" }} onSort={onSort} />)
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
})
