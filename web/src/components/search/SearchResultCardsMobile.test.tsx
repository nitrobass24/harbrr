import { fireEvent, render, screen, waitFor, within } from "@testing-library/react"
import { afterEach, describe, expect, it, vi } from "vitest"
import type { DownloadClient } from "@/lib/api"
import { groupRows, soloGroups } from "./search-group"
import type { SearchRow, Sort } from "./search-sort"
import { SearchResultCardsMobile } from "./SearchResultCardsMobile"

// Grouping OFF: every row is its own card, which is the flat list this suite has always
// asserted (autobrr/harbrr#398 — the toggle must not change it).
const SORT: Sort = { key: "seeders", dir: "desc" }

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
    render(<SearchResultCardsMobile groups={soloGroups(ROWS)} catNames={CATS} sort={SORT} />)

    const bunny = cardFor("Big Buck Bunny 1080p")
    expect(within(bunny).getByText("demotracker")).toBeTruthy()
    expect(within(bunny).getByText("Movies")).toBeTruthy()
    expect(within(bunny).getByText("2.5 GiB")).toBeTruthy()
    expect(within(bunny).getByText("42 seeds")).toBeTruthy()
    expect(within(bunny).getByText("7 leech")).toBeTruthy()
  })

  it("marks freeleech releases with the FL badge", () => {
    render(<SearchResultCardsMobile groups={soloGroups(ROWS)} catNames={CATS} sort={SORT} />)
    expect(within(cardFor("Sintel 720p FL")).getByText("FL")).toBeTruthy()
    expect(within(cardFor("Big Buck Bunny 1080p")).queryByText("FL")).toBeNull()
  })

  it("renders the Grab actions with the href verbatim", () => {
    render(<SearchResultCardsMobile groups={soloGroups(ROWS)} catNames={CATS} sort={SORT} />)
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
    render(<SearchResultCardsMobile groups={soloGroups(rows)} catNames={CATS} sort={SORT} />)
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
    render(<SearchResultCardsMobile groups={soloGroups(ROWS)} catNames={CATS} sort={SORT} />)
    expect(screen.queryByRole("button", { name: /Send .* to a download client/ })).toBeNull()
  })

  it("renders no control for a release whose link was withheld", () => {
    render(
      <SearchResultCardsMobile
        groups={soloGroups([{ indexer: "demotracker", release: { title: "Withheld", size: 1 } }])}
        catNames={CATS}
        sort={SORT}
        clients={[CLIENT]}
      />
    )
    expect(screen.queryByRole("button", { name: /Send .* to a download client/ })).toBeNull()
  })

  it("posts the magnet verbatim for a magnet-only release", async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(null, { status: 204 }))
    vi.stubGlobal("fetch", fetchMock)
    render(<SearchResultCardsMobile groups={soloGroups(ROWS)} catNames={CATS} sort={SORT} clients={[CLIENT]} />)

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
    render(<SearchResultCardsMobile groups={soloGroups(ROWS)} catNames={CATS} sort={SORT} clients={[CLIENT]} />)

    openSendMenu("Big Buck Bunny 1080p")
    fireEvent.click(await screen.findByRole("menuitem", { name: "seedbox" }))

    await waitFor(() => expect(toasted).toContain("Sending to seedbox failed"))
  })
})

// The same release on two trackers: one shared infohash.
const GROUPED: SearchRow[] = [
  {
    indexer: "demotracker",
    protocol: "torrent",
    release: {
      title: "Tears of Steel 1080p",
      link: "http://tracker.example/dl?id=9&passkey=NOTREAL",
      infohash: "ABCDEF",
      size: 4_294_967_296,
      categories: [2000],
      seeders: 3,
      leechers: 6,
      publishDate: "2026-07-20T00:00:00Z",
      downloadVolumeFactor: 0, // freeleech, but NOT the collapsed row's representative
    },
  },
  {
    indexer: "demopublic",
    protocol: "torrent",
    release: {
      title: "tears.of.steel.1080p",
      magnet: "magnet:?xt=urn:btih:abcdef",
      infohash: "abcdef",
      size: 4_294_967_296,
      categories: [2000],
      seeders: 91,
    },
  },
]

describe("SearchResultCardsMobile — grouped view (autobrr/harbrr#398)", () => {
  it("renders one card per release, badged with every tracker and its source count", () => {
    render(<SearchResultCardsMobile groups={groupRows(GROUPED)} catNames={CATS} sort={SORT} />)

    const card = cardFor("tears.of.steel.1080p") // best member under seeders desc
    expect(within(card).getByText("demotracker")).toBeTruthy()
    expect(within(card).getByText("demopublic")).toBeTruthy()
    expect(within(card).getByText("(2)")).toBeTruthy()
    expect(within(card).getByText("91 seeds")).toBeTruthy()
    expect(screen.queryByRole("link")).toBeNull()
    // The representative is not the freeleech one, so nothing claims FL while collapsed.
    expect(within(card).queryByText("FL")).toBeNull()
  })

  // A member entry is scoped by its own grab action, whose label names the tracker —
  // so each assertion below is about THAT member's row, not about the card at large.
  const memberRow = (indexer: string) =>
    screen.getByLabelText(new RegExp(`from ${indexer}$`)).closest<HTMLElement>("div.flex-col")!

  it("expands to each tracker's own entry, each individually grabbable", () => {
    render(<SearchResultCardsMobile groups={groupRows(GROUPED)} catNames={CATS} sort={SORT} />)

    fireEvent.click(screen.getByRole("button", { name: /Expand .* 2 sources/ }))

    expect(screen.getByLabelText("Download Tears of Steel 1080p from demotracker").getAttribute("href"))
      .toBe("http://tracker.example/dl?id=9&passkey=NOTREAL")
    expect(screen.getByLabelText("Magnet for tears.of.steel.1080p from demopublic").getAttribute("href"))
      .toBe("magnet:?xt=urn:btih:abcdef")

    // Each member states the same facts a card of its own would, on its OWN row — a
    // freeleech member the operator cannot see is a member they cannot pick on.
    const freeleech = within(memberRow("demotracker"))
    expect(freeleech.getByText("FL")).toBeTruthy()
    expect(freeleech.getByText("Movies")).toBeTruthy()
    expect(freeleech.getByText("4.0 GiB")).toBeTruthy()
    expect(freeleech.getByText("3 seeds")).toBeTruthy()
    expect(freeleech.getByText("6 leech")).toBeTruthy()
    expect(freeleech.getByText(/\d+d ago/)).toBeTruthy()

    const paid = within(memberRow("demopublic"))
    expect(paid.queryByText("FL")).toBeNull() // its own FL state, not the group's
    expect(paid.getByText("Movies")).toBeTruthy()
    expect(paid.getByText("4.0 GiB")).toBeTruthy()
    expect(paid.getByText("91 seeds")).toBeTruthy()
    expect(paid.getByText("0 leech")).toBeTruthy()
    expect(paid.queryByText(/\d+d ago/)).toBeNull() // it carries no publish date

    fireEvent.click(screen.getByRole("button", { name: /Collapse .* 2 sources/ }))

    // Collapsed, only the representative's own facts remain: everything the members
    // alone contributed is gone.
    expect(screen.queryByLabelText(/from demotracker$/)).toBeNull()
    expect(screen.queryByLabelText(/from demopublic$/)).toBeNull()
    expect(screen.queryByText("FL")).toBeNull()
    expect(screen.queryByText("3 seeds")).toBeNull()
    expect(screen.queryByText("6 leech")).toBeNull()
    expect(screen.queryByText(/\d+d ago/)).toBeNull()
    expect(screen.getAllByText("Movies")).toHaveLength(1)
    expect(screen.getAllByText("4.0 GiB")).toHaveLength(1)
    expect(screen.getByText("91 seeds")).toBeTruthy() // the representative's own
  })

  it("renders exactly the flat list when nothing groups — same cards, same order", () => {
    const { unmount } = render(<SearchResultCardsMobile groups={groupRows(ROWS)} catNames={CATS} sort={SORT} />)
    const grouped = screen.getAllByRole("heading").map((h) => h.closest("div.rounded-lg")!.textContent)
    unmount()

    render(<SearchResultCardsMobile groups={soloGroups(ROWS)} catNames={CATS} sort={SORT} />)
    expect(screen.getAllByRole("heading").map((h) => h.closest("div.rounded-lg")!.textContent)).toEqual(grouped)
  })
})
