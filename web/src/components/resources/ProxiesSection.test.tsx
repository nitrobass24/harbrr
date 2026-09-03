import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { fireEvent, render, screen } from "@testing-library/react"
import { describe, expect, it, vi } from "vitest"
import type { ReactNode } from "react"
import type { Proxy } from "@/lib/api"
import { stubApi } from "@/test/stubApi"
import { ProxiesSection } from "./ProxiesSection"

const PROXY: Proxy = {
  id: 3, name: "home", type: "socks5", host: "10.0.0.9", port: 1080, username: "alice",
  createdAt: "2026-07-01T00:00:00Z", updatedAt: "2026-07-01T00:00:00Z",
}

interface PatchProxyBody {
  host?: string
  password?: string
}

function wrap(children: ReactNode) {
  return (
    <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      {children}
    </QueryClientProvider>
  )
}

// Stubs GET /api/proxies with PROXY and captures the PATCH body sent on save.
function stubFetchAndCapturePatch(): { patchBody: () => Promise<PatchProxyBody> } {
  const api = stubApi({
    "GET /api/proxies": [PROXY],
    "PATCH /api/proxies/{id}": {},
  })
  return {
    patchBody: async () => {
      const call = await vi.waitFor(() => {
        const found = api.calls("PATCH /api/proxies/{id}")[0]
        if (!found) throw new Error("no PATCH call yet")
        return found
      })
      return JSON.parse(await call.text()) as PatchProxyBody
    },
  }
}

describe("ProxiesSection", () => {
  // Representative failed-list case for the six ResourceSection adopters — the
  // pending/error/empty states themselves are covered by resource-section.test.tsx.
  it("a failed list query shows LoadError, not an empty card", async () => {
    stubApi({ "GET /api/proxies": () => Response.json({ error: "boom" }, { status: 500 }) })
    render(wrap(<ProxiesSection />))

    expect((await screen.findByRole("alert")).textContent).toContain("Loading proxies failed")
    expect(screen.queryByText(/No proxies/)).toBeNull()
  })

  it("lists a proxy's host:port plainly (no masking)", async () => {
    stubApi({ "GET /api/proxies": [PROXY] })
    render(wrap(<ProxiesSection />))

    expect(await screen.findByText("home")).toBeTruthy()
    expect(screen.getByText("10.0.0.9:1080")).toBeTruthy()
  })

  it("edit: an untyped password omits the field, keeping the stored one", async () => {
    const { patchBody } = stubFetchAndCapturePatch()
    render(wrap(<ProxiesSection />))

    fireEvent.click(await screen.findByLabelText("Edit home"))
    fireEvent.click(await screen.findByRole("button", { name: "Save changes" }))

    const body = await patchBody()
    expect(body.password).toBeUndefined()
    expect(body.host).toBe("10.0.0.9")
  })

  it("edit: a typed password rotates it", async () => {
    const { patchBody } = stubFetchAndCapturePatch()
    render(wrap(<ProxiesSection />))

    fireEvent.click(await screen.findByLabelText("Edit home"))
    fireEvent.change(await screen.findByLabelText(/Password/), { target: { value: "new-secret" } })
    fireEvent.click(screen.getByRole("button", { name: "Save changes" }))

    const body = await patchBody()
    expect(body.password).toBe("new-secret")
  })
})
