import { beforeEach, describe, expect, it, vi } from "vitest"
import { notifyError, notifyInfo, notifySuccess, notifyWarn } from "./notify"
import { api, type ApiClient } from "@/lib/api"
import { stubApi } from "@/test/stubApi"

const { toastError, toastWarning, toastSuccess, toastInfo } = vi.hoisted(() => ({
  toastError: vi.fn(),
  toastWarning: vi.fn(),
  toastSuccess: vi.fn(),
  toastInfo: vi.fn(),
}))

vi.mock("sonner", () => ({
  toast: { error: toastError, warning: toastWarning, success: toastSuccess, info: toastInfo },
}))

const SHIP = "POST /api/logs/frontend"

// Shipping is fire-and-forget, so absence can only be asserted after giving a
// (wrongly) fired request time to land.
function settle(): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, 20))
}

describe("notify", () => {
  beforeEach(() => {
    toastError.mockClear()
    toastWarning.mockClear()
    toastSuccess.mockClear()
    toastInfo.mockClear()
  })

  it("notifyError shows the toast and ships error level with the error's message as context", async () => {
    const stub = stubApi({ [SHIP]: null })
    notifyError("Save failed", new Error("network down"))
    expect(toastError).toHaveBeenCalledWith("Save failed")
    await vi.waitFor(() => expect(stub.calls(SHIP)).toHaveLength(1))
    expect(await stub.calls(SHIP)[0].json()).toEqual({ level: "error", message: "Save failed", context: "network down" })
  })

  it("notifyError ships without context when no error is passed", async () => {
    const stub = stubApi({ [SHIP]: null })
    notifyError("Save failed")
    expect(toastError).toHaveBeenCalledWith("Save failed")
    await vi.waitFor(() => expect(stub.calls(SHIP)).toHaveLength(1))
    expect(await stub.calls(SHIP)[0].json()).toEqual({ level: "error", message: "Save failed" })
  })

  it("notifyError never stringifies a non-Error value into context", async () => {
    const stub = stubApi({ [SHIP]: null })
    notifyError("Save failed", { status: 500, body: "sensitive response" })
    await vi.waitFor(() => expect(stub.calls(SHIP)).toHaveLength(1))
    expect(await stub.calls(SHIP)[0].json()).toEqual({ level: "error", message: "Save failed" })
  })

  it("notifyWarn shows the toast and ships warn level with context", async () => {
    const stub = stubApi({ [SHIP]: null })
    notifyWarn("Slow response", new Error("timeout"))
    expect(toastWarning).toHaveBeenCalledWith("Slow response")
    await vi.waitFor(() => expect(stub.calls(SHIP)).toHaveLength(1))
    expect(await stub.calls(SHIP)[0].json()).toEqual({ level: "warn", message: "Slow response", context: "timeout" })
  })

  it("notifySuccess shows the toast and never ships", async () => {
    const stub = stubApi({ [SHIP]: null })
    notifySuccess("Indexer deleted")
    expect(toastSuccess).toHaveBeenCalledWith("Indexer deleted")
    await settle()
    expect(stub.calls(SHIP)).toHaveLength(0)
  })

  it("notifyInfo shows the toast and never ships", async () => {
    const stub = stubApi({ [SHIP]: null })
    notifyInfo("Sync scheduled")
    expect(toastInfo).toHaveBeenCalledWith("Sync scheduled")
    await settle()
    expect(stub.calls(SHIP)).toHaveLength(0)
  })

  it("swallows a shipping failure without throwing or surfacing a second toast", async () => {
    const stub = stubApi({ [SHIP]: () => Response.json({ error: "logging endpoint unreachable", code: "internal" }, { status: 500 }) })
    expect(() => notifyError("Save failed")).not.toThrow()
    await vi.waitFor(() => expect(stub.calls(SHIP)).toHaveLength(1))
    await settle()
    // No follow-up error toast from the failed shipment — exactly one toast.error call.
    expect(toastError).toHaveBeenCalledTimes(1)
  })

  it("no-ops instead of throwing when the api client is missing http (partial test mock)", () => {
    const mutableApi = api as unknown as { http?: ApiClient["http"] }
    const original = mutableApi.http
    mutableApi.http = undefined
    try {
      expect(() => notifyError("Save failed")).not.toThrow()
      expect(toastError).toHaveBeenCalledWith("Save failed")
    } finally {
      mutableApi.http = original
    }
  })
})
