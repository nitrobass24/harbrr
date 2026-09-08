import { beforeEach, describe, expect, it, vi } from "vitest"
import { notifyError, notifySuccess, notifyWarn } from "./notify"
import { stubApi } from "@/test/stubApi"

const { toastError, toastWarning, toastSuccess } = vi.hoisted(() => ({
  toastError: vi.fn(),
  toastWarning: vi.fn(),
  toastSuccess: vi.fn(),
}))

vi.mock("sonner", () => ({
  toast: { error: toastError, warning: toastWarning, success: toastSuccess },
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

  it("swallows a shipping failure without throwing or surfacing a second toast", async () => {
    const stub = stubApi({ [SHIP]: () => Response.json({ error: "logging endpoint unreachable", code: "internal" }, { status: 500 }) })
    expect(() => notifyError("Save failed")).not.toThrow()
    await vi.waitFor(() => expect(stub.calls(SHIP)).toHaveLength(1))
    await settle()
    // No follow-up error toast from the failed shipment — exactly one toast.error call.
    expect(toastError).toHaveBeenCalledTimes(1)
  })
})
