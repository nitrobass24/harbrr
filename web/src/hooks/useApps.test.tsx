import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { renderHook, waitFor } from "@testing-library/react"
import { beforeEach, describe, expect, it, vi } from "vitest"
import { type ReactNode } from "react"
import { useDeleteApp } from "./useApps"
import { APIError } from "@/lib/api"
import { stubApi } from "@/test/stubApi"

const { notifyErrorMock } = vi.hoisted(() => ({
  notifyErrorMock: vi.fn(),
}))
vi.mock("@/lib/notify", () => ({ notifyError: notifyErrorMock }))

function wrapper({ children }: { children: ReactNode }) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return <QueryClientProvider client={qc}>{children}</QueryClientProvider>
}

describe("useDeleteApp", () => {
  beforeEach(() => {
    notifyErrorMock.mockClear()
  })

  it("surfaces the server's 409 conflict message in the toast (not a generic string)", async () => {
    stubApi({
      "DELETE /api/apps/{id}": () =>
        Response.json({ error: "app is in use by 2 app-sync connections", code: "conflict" }, { status: 409 }),
    })

    const { result } = renderHook(() => useDeleteApp(), { wrapper })
    result.current.mutate(1)

    await waitFor(() => expect(result.current.isError).toBe(true))
    expect(notifyErrorMock).toHaveBeenCalledWith(
      "Deleting the app failed: app is in use by 2 app-sync connections",
      expect.any(APIError)
    )
  })

  it("does not toast on success", async () => {
    stubApi({ "DELETE /api/apps/{id}": null })

    const { result } = renderHook(() => useDeleteApp(), { wrapper })
    result.current.mutate(1)

    await waitFor(() => expect(result.current.isSuccess).toBe(true))
    expect(notifyErrorMock).not.toHaveBeenCalled()
  })
})
