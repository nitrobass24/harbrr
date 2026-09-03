import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { fireEvent, render, screen, waitFor } from "@testing-library/react"
import { describe, expect, it } from "vitest"
import type { ReactNode } from "react"
import { stubApi } from "@/test/stubApi"
import { SystemSection } from "./SystemSection"

function wrap(children: ReactNode) {
  return (
    <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      {children}
    </QueryClientProvider>
  )
}

// stubFetch answers the adult-categories endpoint from a mutable cell (so a PUT is
// visible to the follow-up GET) and every other settings probe with an inert body.
function stubFetch(state: { hidden: boolean }) {
  const puts: boolean[] = []
  stubApi({
    "GET /api/config/adult-categories": () => Response.json(state),
    "PUT /api/config/adult-categories": async (request: Request) => {
      const body = (await request.json()) as { hidden: boolean }
      puts.push(body.hidden)
      state.hidden = body.hidden
      return Response.json(state)
    },
    "GET /api/config/log-level": { level: "info" },
    "GET /api/auth/me": () => Response.json({}, { status: 401 }),
    "GET /healthz": { version: "test", commit: "abc" },
  })
  return puts
}

describe("SystemSection hide-adult-categories toggle", () => {
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
