import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { renderHook, waitFor } from "@testing-library/react"
import { beforeEach, describe, expect, it, vi } from "vitest"
import { type ReactNode } from "react"
import { useDeleteApp } from "./useApps"
import { APIError } from "@/lib/api"

const { notifyErrorMock, deleteAppMock } = vi.hoisted(() => ({
  notifyErrorMock: vi.fn(),
  deleteAppMock: vi.fn(),
}))
vi.mock("@/lib/notify", () => ({ notifyError: notifyErrorMock }))
vi.mock("@/lib/api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/api")>()
  return { ...actual, api: { deleteApp: deleteAppMock } }
})

function wrapper({ children }: { children: ReactNode }) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return <QueryClientProvider client={qc}>{children}</QueryClientProvider>
}

describe("useDeleteApp", () => {
  beforeEach(() => {
    notifyErrorMock.mockClear()
    deleteAppMock.mockReset()
  })

  it("surfaces the server's 409 conflict message in the toast (not a generic string)", async () => {
    deleteAppMock.mockRejectedValue(new APIError(409, "conflict", "app is in use by 2 app-sync connections"))

    const { result } = renderHook(() => useDeleteApp(), { wrapper })
    result.current.mutate(1)

    await waitFor(() => expect(result.current.isError).toBe(true))
    expect(notifyErrorMock).toHaveBeenCalledWith(
      "Deleting the app failed: app is in use by 2 app-sync connections",
      expect.any(APIError)
    )
  })

  it("does not toast on success", async () => {
    deleteAppMock.mockResolvedValue(undefined)

    const { result } = renderHook(() => useDeleteApp(), { wrapper })
    result.current.mutate(1)

    await waitFor(() => expect(result.current.isSuccess).toBe(true))
    expect(notifyErrorMock).not.toHaveBeenCalled()
  })
})
