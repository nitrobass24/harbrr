import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { fireEvent, render, screen, waitFor } from "@testing-library/react"
import { afterEach, describe, expect, it, vi } from "vitest"
import type { ReactNode } from "react"
import type { AnnounceConnection } from "@/types/api"
import { AnnounceSection } from "./AnnounceSection"

const TARGET: AnnounceConnection = {
  id: 1,
  name: "qui-main",
  kind: "qui",
  baseUrl: "http://qui:7476",
  harbrrUrl: "http://harbrr:7478",
  apiKey: "<redacted>",
  enabled: true,
  createdAt: "2026-07-01T00:00:00Z",
  updatedAt: "2026-07-01T00:00:00Z",
}

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(status === 204 ? null : JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  })
}

function wrap(children: ReactNode) {
  return (
    <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      {children}
    </QueryClientProvider>
  )
}

describe("AnnounceSection edit", () => {
  afterEach(() => vi.unstubAllGlobals())

  it("omits apiKey when the key field is left blank (keep the stored key)", async () => {
    const fetchMock = vi.fn((url: string | URL, init?: RequestInit) => {
      const u = String(url)
      const method = init?.method ?? "GET"
      if (u.includes("/announce-connections") && method === "PATCH") return Promise.resolve(jsonResponse(null, 204))
      if (u.includes("/announce-connections")) return Promise.resolve(jsonResponse([TARGET]))
      if (u.includes("/server-info")) return Promise.resolve(jsonResponse({ port: 7478 }))
      return Promise.resolve(jsonResponse([]))
    })
    vi.stubGlobal("fetch", fetchMock)

    render(wrap(<AnnounceSection />))

    fireEvent.click(await screen.findByRole("button", { name: "Edit qui-main" }))
    // The edit form is seeded from the existing target; the key field starts blank.
    const nameInput = await screen.findByLabelText<HTMLInputElement>("Name")
    expect(nameInput.value).toBe("qui-main")
    fireEvent.click(screen.getByRole("button", { name: "Save changes" }))

    await waitFor(() => {
      const patch = fetchMock.mock.calls.find(([, init]) => init?.method === "PATCH")
      expect(patch).toBeTruthy()
      const body = JSON.parse((patch![1] as RequestInit).body as string) as Record<string, unknown>
      expect(body).toMatchObject({ name: "qui-main", baseUrl: "http://qui:7476", harbrrUrl: "http://harbrr:7478" })
      expect(body).not.toHaveProperty("apiKey")
    })
  })
})
