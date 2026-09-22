import { fireEvent, render, screen } from "@testing-library/react"
import { describe, expect, it } from "vitest"
import { groupRows, soloGroups } from "./search-group"
import type { SearchRow } from "./search-sort"
import { SearchResultCardsMobile } from "./SearchResultCardsMobile"
import { SearchResultsTable } from "./SearchResultsTable"

describe.each(["desktop", "mobile"])("%s result title links", (surface) => {
  function renderResults(rows: SearchRow[], grouped = false) {
    const props = {
      groups: grouped ? groupRows(rows) : soloGroups(rows),
      catNames: new Map<number, string>(),
      sort: { key: "seeders", dir: "desc" } as const,
    }
    if (surface === "desktop") return render(<SearchResultsTable {...props} onSort={() => {}} />)
    return render(<SearchResultCardsMobile {...props} />)
  }

  it.each(["http", "https"])("links titles to %s source pages and preserves download links", (scheme) => {
    const details = `${scheme}://tracker.example/details?id=42&view=full#files`
    const download = "https://tracker.example/download/42"
    renderResults([{ indexer: "tracker", release: { title: "Release", details, link: download } }])

    const title = screen.getByRole("link", { name: "Release" })
    expect(title.getAttribute("href")).toBe(details)
    expect(title.getAttribute("target")).toBe("_blank")
    expect(title.getAttribute("rel")).toBe("noopener noreferrer")
    expect(screen.getByRole("link", { name: "Download Release" }).getAttribute("href")).toBe(download)
  })

  it.each([undefined, "", "not a URL", "/details/42", "javascript:alert(1)", "java\tscript:alert(1)", "data:text/html,test", "magnet:?xt=urn:btih:abc"])(
    "keeps the title as text for unavailable or unsafe details %s",
    (details) => {
      renderResults([{ indexer: "tracker", release: { title: "Release", details, link: "https://tracker.example/download/42" } }])
      expect(screen.getByText("Release")).toBeTruthy()
      expect(screen.queryByRole("link", { name: "Release" })).toBeNull()
      expect(screen.getByRole("link", { name: "Download Release" })).toBeTruthy()
    }
  )

  it("links the representative and each expanded member to their own source", () => {
    const rows: SearchRow[] = [
      { indexer: "first", protocol: "torrent", release: { title: "First title", infohash: "abcdef", seeders: 10, details: "https://first.example/details/1" } },
      { indexer: "second", protocol: "torrent", release: { title: "Second title", infohash: "abcdef", seeders: 20, details: "https://second.example/details/2" } },
    ]
    renderResults(rows, true)
    expect(screen.getByRole("link", { name: "Second title" }).getAttribute("href")).toBe(rows[1].release.details)
    expect(screen.queryByRole("link", { name: "First title" })).toBeNull()

    fireEvent.click(screen.getByRole("button", { name: /Expand .* 2 sources/ }))
    expect(screen.getByRole("link", { name: "First title" }).getAttribute("href")).toBe(rows[0].release.details)
    const second = screen.getAllByRole("link", { name: "Second title" })
    expect(second).toHaveLength(2)
    for (const title of second) expect(title.getAttribute("href")).toBe(rows[1].release.details)
  })
})
