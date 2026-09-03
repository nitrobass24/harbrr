import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { fireEvent, render, screen, waitFor } from "@testing-library/react"
import { describe, expect, it } from "vitest"
import type { ReactNode } from "react"
import { stubApi } from "@/test/stubApi"
import { ApiKeysSection } from "./ApiKeysSection"

const MINTED = {
  id: 7,
  name: "sonarr",
  key: "hbr_PLAINTEXT_SHOWN_ONCE",
  createdAt: "2026-07-03T00:00:00Z",
}

function wrap(children: ReactNode) {
  return (
    <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      {children}
    </QueryClientProvider>
  )
}

describe("ApiKeysSection mint dialog", () => {
  it("a failed list query shows LoadError, not an empty card", async () => {
    stubApi({ "GET /api/apikeys": () => Response.json({ error: "boom" }, { status: 500 }) })
    render(wrap(<ApiKeysSection />))

    expect((await screen.findByRole("alert")).textContent).toContain("Loading API keys failed")
    expect(screen.queryByText(/No keys yet/)).toBeNull()
  })

  it("shows the plaintext key exactly once and never re-renders it after closing", async () => {
    stubApi({
      "GET /api/apikeys": [],
      "POST /api/apikeys": () => Response.json(MINTED, { status: 201 }),
    })
    render(wrap(<ApiKeysSection />))

    fireEvent.change(screen.getByPlaceholderText(/Key name/), { target: { value: "sonarr" } })
    fireEvent.click(screen.getByRole("button", { name: /Mint key/ }))

    // The one-time dialog shows the plaintext.
    const key = await screen.findByTestId("minted-key")
    expect(key.textContent).toBe("hbr_PLAINTEXT_SHOWN_ONCE")

    // Close it — the plaintext is gone from the DOM and nothing can bring it back.
    fireEvent.keyDown(document.body, { key: "Escape" })
    await waitFor(() => {
      expect(screen.queryByTestId("minted-key")).toBeNull()
    })
    expect(screen.queryByText("hbr_PLAINTEXT_SHOWN_ONCE")).toBeNull()
  })
})
