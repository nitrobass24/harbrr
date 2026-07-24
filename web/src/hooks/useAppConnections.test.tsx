import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { renderHook, waitFor } from "@testing-library/react"
import { beforeEach, describe, expect, it, vi } from "vitest"
import { type ReactNode } from "react"
import { useSetConnectionEnabled } from "./useAppConnections"
import type { AppConnection } from "@/lib/api"

const { toastError, setConnectionEnabledMock } = vi.hoisted(() => ({
  toastError: vi.fn(),
  setConnectionEnabledMock: vi.fn(),
}))
vi.mock("sonner", () => ({
  toast: { error: toastError },
}))
vi.mock("@/lib/api", () => ({
  api: { setConnectionEnabled: setConnectionEnabledMock },
}))

function makeConnection(overrides: Partial<AppConnection> = {}): AppConnection {
  return {
    id: 1,
    name: "sonarr",
    kind: "sonarr",
    baseUrl: "http://sonarr:8989",
    harbrrUrl: "http://harbrr:7478",
    enabled: true,
    syncLevel: "full",
    indexScope: "all",
    freeleechMode: "honor",
    createdAt: "2026-01-01T00:00:00Z",
    updatedAt: "2026-01-01T00:00:00Z",
    ...overrides,
  }
}

// Seed ["app-connections"] in a shared client and render the toggle mutation
// against it so the test can inspect the exact cache the hook reads/writes.
function renderSetEnabled() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  qc.setQueryData<AppConnection[]>(["app-connections"], [makeConnection({ enabled: true })])
  const wrapper = ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={qc}>{children}</QueryClientProvider>
  )
  const { result } = renderHook(() => useSetConnectionEnabled(), { wrapper })
  const enabledOf = () => qc.getQueryData<AppConnection[]>(["app-connections"])?.[0].enabled
  return { result, enabledOf }
}

describe("useSetConnectionEnabled optimistic rollback", () => {
  beforeEach(() => {
    toastError.mockClear()
    setConnectionEnabledMock.mockReset()
  })

  it("rolls back the optimistic flip when the request rejects", async () => {
    // Keep the request pending so the optimistic state is observable before we reject.
    let reject!: (e: Error) => void
    setConnectionEnabledMock.mockReturnValue(new Promise((_r, rj) => { reject = rj }))

    const { result, enabledOf } = renderSetEnabled()
    result.current.mutate({ id: 1, enabled: false })

    // onMutate flips the cached switch off immediately (optimistic).
    await waitFor(() => expect(enabledOf()).toBe(false))

    reject(new Error("nope"))

    // onError restores the pre-mutation snapshot — the rollback.
    await waitFor(() => expect(enabledOf()).toBe(true))
    expect(toastError).toHaveBeenCalledWith("Disabling the connection failed")
  })

  it("keeps the optimistic flip when the request resolves", async () => {
    setConnectionEnabledMock.mockResolvedValue(undefined)

    const { result, enabledOf } = renderSetEnabled()
    result.current.mutate({ id: 1, enabled: false })

    await waitFor(() => expect(result.current.isSuccess).toBe(true))
    // Success path never rolls back: the flip stays applied.
    expect(enabledOf()).toBe(false)
    expect(toastError).not.toHaveBeenCalled()
  })
})
