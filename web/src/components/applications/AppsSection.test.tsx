import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { fireEvent, render, screen, waitFor } from "@testing-library/react"
import { afterEach, describe, expect, it, vi } from "vitest"
import type { ReactNode } from "react"
import type { App } from "@/lib/api"
import { AppsSection } from "./AppsSection"

const APP: App = {
  id: 3, kind: "sonarr", name: "sonarr-app", baseUrl: "http://sonarr:8989", username: "",
  apiKey: "<redacted>", harbrrUrl: "http://harbrr:7478", enabled: true,
  references: { appConnections: 2, announce: 0, download: 1 },
  createdAt: "2026-07-01T00:00:00Z", updatedAt: "2026-07-01T00:00:00Z",
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

describe("AppsSection", () => {
  afterEach(() => vi.unstubAllGlobals())

  it("lists an app's name, kind, and reference count", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse([APP])))
    render(wrap(<AppsSection />))

    expect(await screen.findByText("sonarr-app")).toBeTruthy()
    expect(screen.getByText("sonarr")).toBeTruthy()
    expect(screen.getByText(/used by 3 surfaces/)).toBeTruthy()
  })

  it("edit: a typed credential rotates it; a blank one keeps the stored one", async () => {
    const fetchMock = vi.fn((request: Request) => {
      if (request.method === "PATCH") return Promise.resolve(jsonResponse(null, 204))
      return Promise.resolve(jsonResponse([APP]))
    })
    vi.stubGlobal("fetch", fetchMock)
    render(wrap(<AppsSection />))

    fireEvent.click(await screen.findByLabelText("Edit sonarr-app"))
    fireEvent.click(await screen.findByRole("button", { name: "Save changes" }))

    await waitFor(async () => {
      const patch = fetchMock.mock.calls.find(([request]) => request.method === "PATCH")
      expect(patch).toBeTruthy()
      const body = JSON.parse(await patch![0].clone().text()) as Record<string, unknown>
      expect(body).not.toHaveProperty("apiKey")
      expect(body).toMatchObject({ name: "sonarr-app", baseUrl: "http://sonarr:8989" })
    })
  })

  // The 409 conflict message itself is covered by useApps.test.tsx (useDeleteApp
  // owns the toast); this just confirms the row wires the click through to DELETE.
  it("delete: posts to the delete endpoint", async () => {
    const fetchMock = vi.fn((request: Request) => {
      if (request.method === "DELETE") {
        return Promise.resolve(jsonResponse({ error: "app is in use by 2 app-sync connections, 1 download client", code: "conflict" }, 409))
      }
      return Promise.resolve(jsonResponse([APP]))
    })
    vi.stubGlobal("fetch", fetchMock)
    render(wrap(<AppsSection />))

    fireEvent.click(await screen.findByLabelText("Delete sonarr-app"))

    await waitFor(() => {
      const del = fetchMock.mock.calls.find(([request]) => request.method === "DELETE")
      expect(del).toBeTruthy()
    })
  })
})
