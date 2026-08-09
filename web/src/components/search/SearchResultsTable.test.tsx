import { fireEvent, render, screen, waitFor, within } from "@testing-library/react"
import { afterEach, describe, expect, it, vi } from "vitest"
import type { DownloadClient } from "@/lib/api"
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

const CLIENT: DownloadClient = {
  id: 5, name: "seedbox", kind: "qbittorrent", enabled: true, host: "http://localhost:8080",
  username: "admin", secret: "<redacted>", settings: {},
  createdAt: "2026-07-01T00:00:00Z", updatedAt: "2026-07-01T00:00:00Z",
}

function renderTable(rows = ROWS, clients: DownloadClient[] = []) {
  const onSort = vi.fn()
  render(
    <SearchResultsTable rows={rows} catNames={CATS} sort={{ key: "seeders", dir: "desc" }} onSort={onSort} clients={clients} />
  )
  return onSort
}

// Radix's DropdownMenuTrigger opens on pointerdown, not click — jsdom has no
// PointerEvent, so a plain fireEvent.click leaves it closed. Fire both.
function openSendMenu(title: string) {
  const trigger = screen.getByRole("button", { name: `Send ${title} to a download client` })
  fireEvent.pointerDown(trigger, { button: 0, pointerId: 1 })
  fireEvent.click(trigger)
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

  it.each([
    ["javascript:", "javascript:fetch('/api/keys',{method:'DELETE'})"],
    ["data:", "data:text/html,<script>alert(1)</script>"],
    ["vbscript:", "vbscript:msgbox(1)"],
    ["tab-obfuscated javascript:", "java\tscript:alert(1)"],
    ["uppercase JavaScript:", "JavaScript:alert(1)"],
  ])("never renders a clickable href for a %s link or magnet value", (_label, malicious) => {
    const rows: SearchRow[] = [
      {
        indexer: "hostile",
        release: {
          title: "Hostile Release",
          link: malicious,
          magnet: malicious,
          size: 1,
        },
      },
    ]
    renderTable(rows)
    expect(screen.queryByLabelText("Download Hostile Release")).toBeNull()
    expect(screen.queryByLabelText("Magnet for Hostile Release")).toBeNull()
  })

  it("marks freeleech releases with the FL badge", () => {
    renderTable()
    const sintel = screen.getByText("Sintel 720p FL").closest("tr")!
    expect(within(sintel).getByText("FL")).toBeTruthy()
    const bunny = screen.getByText("Big Buck Bunny 1080p").closest("tr")!
    expect(within(bunny).queryByText("FL")).toBeNull()
  })
})

describe("SearchResultsTable — send to download client (autobrr/harbrr#7)", () => {
  afterEach(() => vi.unstubAllGlobals())

  it("renders no control when no download client is configured", () => {
    renderTable()
    expect(screen.queryByRole("button", { name: /Send .* to a download client/ })).toBeNull()
  })

  it("renders no control for a release whose link was withheld", () => {
    renderTable([{ indexer: "demotracker", release: { title: "Withheld", size: 1 } }], [CLIENT])
    expect(screen.queryByRole("button", { name: /Send .* to a download client/ })).toBeNull()
  })

  it("posts the release's indexer, verbatim link, and title to the picked client", async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(null, { status: 204 }))
    vi.stubGlobal("fetch", fetchMock)
    renderTable(ROWS, [CLIENT])

    openSendMenu("Big Buck Bunny 1080p")
    fireEvent.click(await screen.findByRole("menuitem", { name: "seedbox" }))

    const req = await waitFor(() => {
      const call = fetchMock.mock.calls.at(0)
      if (!call) throw new Error("no fetch yet")
      return call[0] as Request
    })
    expect(req.url).toContain("/api/download-clients/5/grab")
    expect(await req.json()).toEqual({
      indexer: "demotracker",
      link: "http://tracker.example/dl?id=1&passkey=NOTREAL",
      name: "Big Buck Bunny 1080p",
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
    renderTable(ROWS, [CLIENT])

    openSendMenu("Big Buck Bunny 1080p")
    fireEvent.click(await screen.findByRole("menuitem", { name: "seedbox" }))

    await waitFor(() => expect(toasted).toContain("Sending to seedbox failed"))
    vi.restoreAllMocks()
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
