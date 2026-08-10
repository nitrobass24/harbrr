import { fireEvent, render, screen, waitFor, within } from "@testing-library/react"
import { afterEach, describe, expect, it, vi } from "vitest"
import type { DownloadClient } from "@/lib/api"
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

const CLIENT: DownloadClient = {
  id: 5, name: "seedbox", kind: "qbittorrent", enabled: true, host: "http://localhost:8080",
  username: "admin", secret: "<redacted>", settings: {},
  createdAt: "2026-07-01T00:00:00Z", updatedAt: "2026-07-01T00:00:00Z",
}

// Radix's DropdownMenuTrigger opens on pointerdown, not click — jsdom has no
// PointerEvent, so a plain fireEvent.click leaves it closed. Fire both.
function openSendMenu(title: string) {
  const trigger = screen.getByRole("button", { name: `Send ${title} to a download client` })
  fireEvent.pointerDown(trigger, { button: 0, pointerId: 1 })
  fireEvent.click(trigger)
}

describe("SearchResultCardsMobile — send to download client (autobrr/harbrr#7)", () => {
  afterEach(() => {
    vi.unstubAllGlobals()
    vi.restoreAllMocks()
  })

  it("renders no control when no download client is configured", () => {
    render(<SearchResultCardsMobile rows={ROWS} catNames={CATS} />)
    expect(screen.queryByRole("button", { name: /Send .* to a download client/ })).toBeNull()
  })

  it("renders no control for a release whose link was withheld", () => {
    render(
      <SearchResultCardsMobile
        rows={[{ indexer: "demotracker", release: { title: "Withheld", size: 1 } }]}
        catNames={CATS}
        clients={[CLIENT]}
      />
    )
    expect(screen.queryByRole("button", { name: /Send .* to a download client/ })).toBeNull()
  })

  it("posts the magnet verbatim for a magnet-only release", async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(null, { status: 204 }))
    vi.stubGlobal("fetch", fetchMock)
    render(<SearchResultCardsMobile rows={ROWS} catNames={CATS} clients={[CLIENT]} />)

    openSendMenu("Sintel 720p FL")
    fireEvent.click(await screen.findByRole("menuitem", { name: "seedbox" }))

    const req = await waitFor(() => {
      const call = fetchMock.mock.calls.at(0)
      if (!call) throw new Error("no fetch yet")
      return call[0] as Request
    })
    expect(req.url).toContain("/api/download-clients/5/grab")
    expect(await req.json()).toEqual({
      indexer: "demopublic",
      link: "magnet:?xt=urn:btih:abc",
      name: "Sintel 720p FL",
    })
  })

  it("shows the server's error on a rejected send", async () => {
    const toasted: string[] = []
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(
      new Response(JSON.stringify({ error: "client does not support payload protocol", code: "invalid" }), {
        status: 400, headers: { "Content-Type": "application/json" },
      })
    ))
    const { toast } = await import("sonner")
    vi.spyOn(toast, "error").mockImplementation((msg) => {
      if (typeof msg === "string") toasted.push(msg)
      return ""
    })
    render(<SearchResultCardsMobile rows={ROWS} catNames={CATS} clients={[CLIENT]} />)

    openSendMenu("Big Buck Bunny 1080p")
    fireEvent.click(await screen.findByRole("menuitem", { name: "seedbox" }))

    await waitFor(() => expect(toasted).toContain("Sending to seedbox failed"))
  })
})
