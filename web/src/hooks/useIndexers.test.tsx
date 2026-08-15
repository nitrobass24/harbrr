import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { renderHook, waitFor } from "@testing-library/react"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"
import type { ReactNode } from "react"
import { useProbeVerdict, useSetIndexerEnabled } from "./useIndexers"
import type { IndexerStatus, Instance } from "@/lib/api"

const { toastSuccess, toastError, setIndexerEnabledMock, getIndexerStatusMock } = vi.hoisted(() => ({
  toastSuccess: vi.fn(),
  toastError: vi.fn(),
  setIndexerEnabledMock: vi.fn(),
  getIndexerStatusMock: vi.fn(),
}))
vi.mock("sonner", () => ({
  toast: { success: toastSuccess, error: toastError },
}))
vi.mock("@/lib/api", () => ({
  api: { setIndexerEnabled: setIndexerEnabledMock, getIndexerStatus: getIndexerStatusMock },
}))

function makeIndexer(overrides: Partial<Instance> = {}): Instance {
  return {
    id: 1,
    slug: "mam",
    definitionId: "myanonamouse",
    name: "MyAnonamouse",
    enabled: true,
    protocol: "torrent",
    proxyId: null,
    solverId: null,
    freeleech: false,
    priority: 25,
    minSeeders: 0,
    syncCategories: [],
    enableRss: true,
    enableAutomaticSearch: true,
    enableInteractiveSearch: true,
    expiresAt: "",
    expiryKind: "",
    expiryLifetime: false,
    createdAt: "2026-01-01T00:00:00Z",
    updatedAt: "2026-01-01T00:00:00Z",
    ...overrides,
  }
}

// Seed ["indexers"] in a shared client and render the toggle mutation against it
// so the test can inspect the exact cache the hook reads/writes.
function renderSetEnabled() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  qc.setQueryData<Instance[]>(["indexers"], [makeIndexer({ enabled: true })])
  const wrapper = ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={qc}>{children}</QueryClientProvider>
  )
  const { result } = renderHook(() => useSetIndexerEnabled(), { wrapper })
  const enabledOf = () => qc.getQueryData<Instance[]>(["indexers"])?.[0].enabled
  return { result, enabledOf }
}

describe("useSetIndexerEnabled optimistic rollback", () => {
  beforeEach(() => {
    toastError.mockClear()
    setIndexerEnabledMock.mockReset()
  })

  it("rolls back the optimistic flip when the request rejects", async () => {
    // Keep the request pending so the optimistic state is observable before we reject.
    let reject!: (e: Error) => void
    setIndexerEnabledMock.mockReturnValue(new Promise((_r, rj) => { reject = rj }))

    const { result, enabledOf } = renderSetEnabled()
    result.current.mutate({ slug: "mam", enabled: false })

    // onMutate flips the cached switch off immediately (optimistic).
    await waitFor(() => expect(enabledOf()).toBe(false))

    reject(new Error("nope"))

    // onError restores the pre-mutation snapshot — the rollback.
    await waitFor(() => expect(enabledOf()).toBe(true))
    expect(toastError).toHaveBeenCalledWith("Disabling mam failed")
  })

  it("keeps the optimistic flip when the request resolves", async () => {
    setIndexerEnabledMock.mockResolvedValue(undefined)

    const { result, enabledOf } = renderSetEnabled()
    result.current.mutate({ slug: "mam", enabled: false })

    await waitFor(() => expect(result.current.isSuccess).toBe(true))
    // Success path never rolls back: the flip stays applied.
    expect(enabledOf()).toBe(false)
    expect(toastError).not.toHaveBeenCalled()
  })
})

// makeStatus builds a status payload for the probe-verdict tests.
function makeStatus(overrides: Partial<IndexerStatus> = {}): IndexerStatus {
  return { slug: "mam", status: "unknown", events: [], probing: false, ...overrides }
}

// Render useProbeVerdict against a shared client so the test can inspect the status
// cache the hook seeds (that seeding is what turns the row's badge red).
function renderProbeVerdict() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const wrapper = ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={qc}>{children}</QueryClientProvider>
  )
  const { result } = renderHook(() => useProbeVerdict(), { wrapper })
  return { verdict: result.current, statusOf: () => qc.getQueryData<IndexerStatus>(["indexers", "mam", "status"]) }
}

// The save flow's instant verdict (autobrr/harbrr#484): the sheet fires no test of its
// own, so a wrong passkey has to reach the operator through the SERVER's probe. Fake
// timers drive the poll — no wall-clock waiting.
describe("useProbeVerdict", () => {
  beforeEach(() => {
    toastError.mockClear()
    getIndexerStatusMock.mockReset()
    vi.useFakeTimers()
  })
  afterEach(() => {
    vi.useRealTimers()
  })

  it("waits out the probe and toasts the failure it found", async () => {
    getIndexerStatusMock
      .mockResolvedValueOnce(makeStatus({ probing: true }))
      .mockResolvedValueOnce(makeStatus({
        status: "failing",
        events: [{ kind: "auth_failure", detail: "login refused", occurred_at: "2026-06-01T00:00:00Z" }],
      }))

    const { verdict, statusOf } = renderProbeVerdict()
    const done = verdict("mam")
    await vi.advanceTimersByTimeAsync(600)
    await done

    expect(toastError).toHaveBeenCalledWith("mam: login refused")
    // The badge reads the same key, so it goes red with the toast.
    expect(statusOf()?.status).toBe("failing")
  })

  it("says nothing when the probe passes", async () => {
    getIndexerStatusMock.mockResolvedValue(makeStatus({ status: "healthy" }))

    const { verdict } = renderProbeVerdict()
    await verdict("mam")

    expect(toastError).not.toHaveBeenCalled()
    expect(getIndexerStatusMock).toHaveBeenCalledTimes(1)
  })

  it("falls back to an honest generic when the failure carries no event", async () => {
    // Rule 3's failing: queried, never succeeded — nothing classified it, so there is no
    // event and no failing-since to quote.
    getIndexerStatusMock.mockResolvedValue(makeStatus({ status: "failing" }))

    const { verdict } = renderProbeVerdict()
    await verdict("mam")

    expect(toastError).toHaveBeenCalledWith("mam: health check failed")
  })
})
