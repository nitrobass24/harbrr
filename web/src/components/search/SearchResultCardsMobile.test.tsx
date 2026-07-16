import { render, screen, within } from "@testing-library/react"
import { describe, expect, it } from "vitest"
import type { SearchRow } from "./search-sort"
import { SearchResultCardsMobile } from "./SearchResultCardsMobile"

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
      downloadVolumeFactor: 0, // freeleech
    },
  },
]

const CATS = new Map([[2000, "Movies"], [5000, "TV"]])

function cardFor(title: string) {
  return screen.getByTitle(title).closest<HTMLElement>("div.rounded-lg")!
}

describe("SearchResultCardsMobile", () => {
  it("renders a card per row with title, indexer, category, size, seeders, and leechers", () => {
    render(<SearchResultCardsMobile rows={ROWS} catNames={CATS} />)

    const bunny = cardFor("Big Buck Bunny 1080p")
    expect(within(bunny).getByText("demotracker")).toBeTruthy()
    expect(within(bunny).getByText("Movies")).toBeTruthy()
    expect(within(bunny).getByText("2.5 GiB")).toBeTruthy()
    expect(within(bunny).getByText("42 seeds")).toBeTruthy()
    expect(within(bunny).getByText("7 leech")).toBeTruthy()
  })

  it("marks freeleech releases with the FL badge", () => {
    render(<SearchResultCardsMobile rows={ROWS} catNames={CATS} />)
    expect(within(cardFor("Sintel 720p FL")).getByText("FL")).toBeTruthy()
    expect(within(cardFor("Big Buck Bunny 1080p")).queryByText("FL")).toBeNull()
  })

  it("renders the Grab actions with the href verbatim", () => {
    render(<SearchResultCardsMobile rows={ROWS} catNames={CATS} />)
    const grab = screen.getByLabelText("Download Big Buck Bunny 1080p")
    expect(grab.getAttribute("href")).toBe("http://tracker.example/dl?id=1&passkey=NOTREAL")
    const magnet = screen.getByLabelText("Magnet for Sintel 720p FL")
    expect(magnet.getAttribute("href")).toBe("magnet:?xt=urn:btih:abc")
  })

  it("never renders a clickable href for an unsafe link or magnet value", () => {
    const rows: SearchRow[] = [
      {
        indexer: "hostile",
        release: {
          title: "Hostile Release",
          link: "javascript:alert(1)",
          magnet: "javascript:alert(1)",
          size: 1,
        },
      },
    ]
    render(<SearchResultCardsMobile rows={rows} catNames={CATS} />)
    expect(screen.queryByLabelText("Download Hostile Release")).toBeNull()
    expect(screen.queryByLabelText("Magnet for Hostile Release")).toBeNull()
  })
})
