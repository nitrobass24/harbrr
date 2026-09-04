import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { render, renderHook, waitFor } from "@testing-library/react"
import { beforeEach, describe, expect, it, vi } from "vitest"
import { useEffect, type ReactNode } from "react"
import { useSetIndexerEnabled, useTestIndexer } from "./useIndexers"
import type { Instance, TestResult } from "@/lib/api"
import { stubApi } from "@/test/stubApi"

const { toastSuccess, toastError } = vi.hoisted(() => ({
  toastSuccess: vi.fn(),
  toastError: vi.fn(),
}))
vi.mock("sonner", () => ({
  toast: { success: toastSuccess, error: toastError },
}))

function wrap(children: ReactNode) {
  return (
    <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      {children}
    </QueryClientProvider>
  )
}

// Stands in for AddIndexerSheet's save-and-test flow: fire the mutation, then
// the caller unmounts it immediately (mirroring onClose() running right after
// test.mutate() in AddIndexerSheet.tsx, before the test settles).
function SaveAndTestProbe({ slug }: { slug: string }) {
  const test = useTestIndexer({ toastResult: true })
  const { mutate } = test
  // eslint-disable-next-line react-hooks/exhaustive-deps -- fire once, like the real save-and-test call site
  useEffect(() => { mutate(slug) }, [])
  return null
}

// stubTest keeps the test request pending until the returned resolve/reject is
// called, so each case controls when the in-flight mutation settles. Both wait
// for the request to actually dispatch first — unlike the old method-level mock,
// the fetch seam is only reached after the client's async middleware runs.
function stubTest() {
  let resolve: ((v: TestResult) => void) | undefined
  let reject: ((e: Error) => void) | undefined
  stubApi({
    "POST /api/indexers/{slug}/test": () =>
      new Promise<Response>((res, rej) => {
        resolve = (v) => res(Response.json(v))
        reject = rej
      }),
  })
  return {
    resolve: async (v: TestResult) => {
      await waitFor(() => expect(resolve).toBeDefined())
      resolve!(v)
    },
    reject: async (e: Error) => {
      await waitFor(() => expect(reject).toBeDefined())
      reject!(e)
    },
  }
}

describe("useTestIndexer toastResult", () => {
  beforeEach(() => {
    toastSuccess.mockClear()
    toastError.mockClear()
  })

  it("toasts a failed test even after the triggering component unmounts", async () => {
    const pending = stubTest()

    const { unmount } = render(wrap(<SaveAndTestProbe slug="mam" />))
    unmount()

    await pending.resolve({ ok: false, error: "bad passkey" })

    await waitFor(() => expect(toastError).toHaveBeenCalledWith("mam: test failed — bad passkey"))
    expect(toastSuccess).not.toHaveBeenCalled()
  })

  it("toasts a passed test even after the triggering component unmounts", async () => {
    const pending = stubTest()

    const { unmount } = render(wrap(<SaveAndTestProbe slug="mam" />))
    unmount()

    await pending.resolve({ ok: true })

    await waitFor(() => expect(toastSuccess).toHaveBeenCalledWith("mam: test passed"))
    expect(toastError).not.toHaveBeenCalled()
  })

  it("toasts a transport failure even after the triggering component unmounts", async () => {
    const pending = stubTest()

    const { unmount } = render(wrap(<SaveAndTestProbe slug="mam" />))
    unmount()

    await pending.reject(new Error("network down"))

    await waitFor(() => expect(toastError).toHaveBeenCalledWith("mam: test request failed"))
    expect(toastSuccess).not.toHaveBeenCalled()
  })
})

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
  })

  it("rolls back the optimistic flip when the request rejects", async () => {
    // Keep the request pending so the optimistic state is observable before we reject.
    let reject: ((e: Error) => void) | undefined
    stubApi({ "POST /api/indexers/{slug}/disable": () => new Promise<Response>((_r, rj) => { reject = rj }) })

    const { result, enabledOf } = renderSetEnabled()
    result.current.mutate({ slug: "mam", enabled: false })

    // onMutate flips the cached switch off immediately (optimistic).
    await waitFor(() => expect(enabledOf()).toBe(false))

    await waitFor(() => expect(reject).toBeDefined())
    reject!(new Error("nope"))

    // onError restores the pre-mutation snapshot — the rollback.
    await waitFor(() => expect(enabledOf()).toBe(true))
    expect(toastError).toHaveBeenCalledWith("Disabling mam failed")
  })

  it("keeps the optimistic flip when the request resolves", async () => {
    stubApi({ "POST /api/indexers/{slug}/disable": null })

    const { result, enabledOf } = renderSetEnabled()
    result.current.mutate({ slug: "mam", enabled: false })

    await waitFor(() => expect(result.current.isSuccess).toBe(true))
    // Success path never rolls back: the flip stays applied.
    expect(enabledOf()).toBe(false)
    expect(toastError).not.toHaveBeenCalled()
  })
})
