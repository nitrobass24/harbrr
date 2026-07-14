import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { fireEvent, render, screen, waitFor } from "@testing-library/react"
import { afterEach, describe, expect, it, vi } from "vitest"
import type { ReactNode } from "react"
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

function stubFetch() {
  const fetchMock = vi.fn().mockImplementation((request: Request) => {
    if (request.method === "POST") {
      return Promise.resolve(new Response(JSON.stringify(MINTED), { status: 201, headers: { "Content-Type": "application/json" } }))
    }
    return Promise.resolve(new Response(JSON.stringify([]), { status: 200, headers: { "Content-Type": "application/json" } }))
  })
  vi.stubGlobal("fetch", fetchMock)
  return fetchMock
}

describe("ApiKeysSection mint dialog", () => {
  afterEach(() => vi.unstubAllGlobals())

  it("shows the plaintext key exactly once and never re-renders it after closing", async () => {
    stubFetch()
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
