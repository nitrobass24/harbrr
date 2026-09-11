import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { fireEvent, render, screen, waitFor, within } from "@testing-library/react"
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
    "GET /api/config/rate-limit": { defaultInterval: "1s" },
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

// The global request-spacing default (autobrr/harbrr#104): read it, save it, and
// say plainly that it can only ever slow harbrr down.
describe("SystemSection default request spacing", () => {
  it("shows the stored default and PUTs an edited one", async () => {
    const puts: string[] = []
    stubApi({
      "GET /api/config/adult-categories": { hidden: false },
      "GET /api/config/log-level": { level: "info" },
      "GET /api/config/rate-limit": { defaultInterval: "1s" },
      "PUT /api/config/rate-limit": async (request: Request) => {
        const body = (await request.json()) as { defaultInterval: string }
        puts.push(body.defaultInterval)
        return Response.json({ defaultInterval: body.defaultInterval })
      },
      "GET /api/auth/me": () => Response.json({}, { status: 401 }),
      "GET /healthz": { version: "test", commit: "abc" },
    })
    render(wrap(<SystemSection />))

    const input = await screen.findByLabelText("Default request spacing")
    await waitFor(() => expect((input as HTMLInputElement).value).toBe("1s"))

    fireEvent.change(input, { target: { value: "5s" } })
    const section = input.closest("section")!
    fireEvent.click(within(section).getByRole("button", { name: "Save" }))
    await waitFor(() => expect(puts).toEqual(["5s"]))

    // The floor is the definition's, not ours — the copy has to say so.
    expect(screen.getByText(/floor that always wins/i)).toBeTruthy()
  })
})
