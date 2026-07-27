import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { fireEvent, render, screen, waitFor } from "@testing-library/react"
import { afterEach, describe, expect, it, vi } from "vitest"
import type { ReactNode } from "react"
import { SystemSection } from "./SystemSection"

function wrap(children: ReactNode) {
  return (
    <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      {children}
    </QueryClientProvider>
  )
}

function json(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } })
}

// stubFetch answers the adult-categories endpoint from a mutable cell (so a PUT is
// visible to the follow-up GET) and every other settings probe with an inert body.
function stubFetch(state: { hidden: boolean }) {
  const puts: boolean[] = []
  const fetchMock = vi.fn().mockImplementation((request: Request) => {
    const url = new URL(request.url, "http://localhost")
    if (url.pathname.endsWith("/api/config/adult-categories")) {
      if (request.method === "PUT") {
        return request.json().then((body: { hidden: boolean }) => {
          puts.push(body.hidden)
          state.hidden = body.hidden
          return json(state)
        })
      }
      return Promise.resolve(json(state))
    }
    if (url.pathname.endsWith("/api/config/log-level")) return Promise.resolve(json({ level: "info" }))
    if (url.pathname.endsWith("/api/auth/me")) return Promise.resolve(json({}, 401))
    if (url.pathname.endsWith("/api/healthz")) return Promise.resolve(json({ version: "test", commit: "abc" }))
    return Promise.resolve(json([]))
  })
  vi.stubGlobal("fetch", fetchMock)
  return puts
}

describe("SystemSection hide-adult-categories toggle", () => {
  afterEach(() => vi.unstubAllGlobals())

  it("reflects the stored setting and PUTs the new value when toggled", async () => {
    const puts = stubFetch({ hidden: false })
    render(wrap(<SystemSection />))

    const toggle = await screen.findByRole("switch", { name: /Hide adult categories/i })
    await waitFor(() => expect(toggle.getAttribute("aria-checked")).toBe("false"))

    fireEvent.click(toggle)
    await waitFor(() => expect(puts).toEqual([true]))
    await waitFor(() => expect(toggle.getAttribute("aria-checked")).toBe("true"))
  })

  it("states the miscategorisation limitation instead of promising a content filter", async () => {
    stubFetch({ hidden: true })
    render(wrap(<SystemSection />))

    await screen.findByRole("switch", { name: /Hide adult categories/i })
    // The honest limitation is required copy (autobrr/harbrr#383): the setting
    // filters by the tracker's declared category and must say so.
    expect(screen.getByText(/filed under something else/i)).toBeTruthy()
    expect(screen.getByText(/not a content filter/i)).toBeTruthy()
  })
})
