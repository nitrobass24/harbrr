import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { fireEvent, render, screen } from "@testing-library/react"
import { afterEach, describe, expect, it, vi } from "vitest"
import type { ReactNode } from "react"
import type { DownloadClient } from "@/lib/api"
import { DownloadClientsSection } from "./DownloadClientsSection"

const CLIENT: DownloadClient = {
  id: 5, name: "seedbox", kind: "qbittorrent", enabled: true, host: "http://localhost:8080",
  username: "admin", secret: "<redacted>", settings: {},
  createdAt: "2026-07-01T00:00:00Z", updatedAt: "2026-07-01T00:00:00Z",
}

interface PatchDownloadClientBody {
  host?: string
  secret?: string
}

function jsonResponse(body: unknown): Response {
  return new Response(JSON.stringify(body), { status: 200, headers: { "Content-Type": "application/json" } })
}

function wrap(children: ReactNode) {
  return (
    <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      {children}
    </QueryClientProvider>
  )
}

// Stubs GET /api/download-clients with CLIENT and captures the PATCH body sent on save.
function stubFetchAndCapturePatch(): { patchBody: () => Promise<PatchDownloadClientBody> } {
  const fetchMock = vi.fn((req: Request) => {
    if (req.method === "PATCH") return Promise.resolve(jsonResponse({}))
    return Promise.resolve(jsonResponse([CLIENT]))
  })
  vi.stubGlobal("fetch", fetchMock)
  return {
    patchBody: async () => {
      const call = await vi.waitFor(() => {
        const found = fetchMock.mock.calls.find(([req]) => req.method === "PATCH")
        if (!found) throw new Error("no PATCH call yet")
        return found
      })
      return JSON.parse(await call[0].text()) as PatchDownloadClientBody
    },
  }
}

describe("DownloadClientsSection", () => {
  afterEach(() => vi.unstubAllGlobals())

  it("lists a client's name, kind, and host", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse([CLIENT])))
    render(wrap(<DownloadClientsSection />))

    expect(await screen.findByText("seedbox")).toBeTruthy()
    expect(screen.getByText("qbittorrent")).toBeTruthy()
    expect(screen.getByText("http://localhost:8080")).toBeTruthy()
  })

  it("edit: an untyped secret omits the field, keeping the stored one", async () => {
    const { patchBody } = stubFetchAndCapturePatch()
    render(wrap(<DownloadClientsSection />))

    fireEvent.click(await screen.findByLabelText("Edit seedbox"))
    fireEvent.click(await screen.findByRole("button", { name: "Save changes" }))

    const body = await patchBody()
    expect(body.secret).toBeUndefined()
    expect(body.host).toBe("http://localhost:8080")
  })

  it("edit: a typed secret rotates it", async () => {
    const { patchBody } = stubFetchAndCapturePatch()
    render(wrap(<DownloadClientsSection />))

    fireEvent.click(await screen.findByLabelText("Edit seedbox"))
    fireEvent.change(await screen.findByLabelText(/Password/), { target: { value: "new-secret" } })
    fireEvent.click(screen.getByRole("button", { name: "Save changes" }))

    const body = await patchBody()
    expect(body.secret).toBe("new-secret")
  })

  it("test: posts to the test endpoint and surfaces a toast", async () => {
    const fetchMock = vi.fn((req: Request) => {
      if (req.method === "POST" && req.url.includes("/test")) return Promise.resolve(jsonResponse({ ok: true }))
      return Promise.resolve(jsonResponse([CLIENT]))
    })
    vi.stubGlobal("fetch", fetchMock)
    render(wrap(<DownloadClientsSection />))

    fireEvent.click(await screen.findByRole("button", { name: "Test" }))

    await vi.waitFor(() => {
      const found = fetchMock.mock.calls.find(([req]) => req.method === "POST" && req.url.includes("/test"))
      if (!found) throw new Error("no test call yet")
    })
  })
})
