import { beforeEach, describe, expect, it, vi } from "vitest"
import { notifyError, notifyInfo, notifySuccess, notifyWarn } from "./notify"
import { api } from "@/lib/api"

// Mirrors the shape lib/api.ts declares for postFrontendLog, without importing the real
// module (which is mocked below) — used only to type the "absent method" test's cast.
type PostFrontendLogFn = (level: "error" | "warn", message: string, context?: string) => Promise<void>

const { toastError, toastWarning, toastSuccess, toastInfo, postFrontendLog } = vi.hoisted(() => ({
  toastError: vi.fn(),
  toastWarning: vi.fn(),
  toastSuccess: vi.fn(),
  toastInfo: vi.fn(),
  postFrontendLog: vi.fn(),
}))

vi.mock("sonner", () => ({
  toast: { error: toastError, warning: toastWarning, success: toastSuccess, info: toastInfo },
}))

vi.mock("@/lib/api", () => ({
  api: { postFrontendLog },
}))

describe("notify", () => {
  beforeEach(() => {
    toastError.mockClear()
    toastWarning.mockClear()
    toastSuccess.mockClear()
    toastInfo.mockClear()
    postFrontendLog.mockReset()
    postFrontendLog.mockResolvedValue(undefined)
  })

  it("notifyError shows the toast and ships error level with the error's message as context", async () => {
    notifyError("Save failed", new Error("network down"))
    expect(toastError).toHaveBeenCalledWith("Save failed")
    await vi.waitFor(() => expect(postFrontendLog).toHaveBeenCalledWith("error", "Save failed", "network down"))
  })

  it("notifyError ships without context when no error is passed", async () => {
    notifyError("Save failed")
    expect(toastError).toHaveBeenCalledWith("Save failed")
    await vi.waitFor(() => expect(postFrontendLog).toHaveBeenCalledWith("error", "Save failed", undefined))
  })

  it("notifyError never stringifies a non-Error value into context", async () => {
    notifyError("Save failed", { status: 500, body: "sensitive response" })
    await vi.waitFor(() => expect(postFrontendLog).toHaveBeenCalledWith("error", "Save failed", undefined))
  })

  it("notifyWarn shows the toast and ships warn level with context", async () => {
    notifyWarn("Slow response", new Error("timeout"))
    expect(toastWarning).toHaveBeenCalledWith("Slow response")
    await vi.waitFor(() => expect(postFrontendLog).toHaveBeenCalledWith("warn", "Slow response", "timeout"))
  })

  it("notifySuccess shows the toast and never ships", async () => {
    notifySuccess("Indexer deleted")
    expect(toastSuccess).toHaveBeenCalledWith("Indexer deleted")
    // Give any (wrongly) fired fire-and-forget call a turn to land before asserting absence.
    await Promise.resolve()
    expect(postFrontendLog).not.toHaveBeenCalled()
  })

  it("notifyInfo shows the toast and never ships", async () => {
    notifyInfo("Sync scheduled")
    expect(toastInfo).toHaveBeenCalledWith("Sync scheduled")
    await Promise.resolve()
    expect(postFrontendLog).not.toHaveBeenCalled()
  })

  it("swallows a shipping failure without throwing or surfacing a second toast", async () => {
    postFrontendLog.mockRejectedValueOnce(new Error("logging endpoint unreachable"))
    expect(() => notifyError("Save failed")).not.toThrow()
    await vi.waitFor(() => expect(postFrontendLog).toHaveBeenCalled())
    // No follow-up error toast from the failed shipment — exactly one toast.error call.
    expect(toastError).toHaveBeenCalledTimes(1)
  })

  it("no-ops instead of throwing when the api client is missing postFrontendLog (partial test mock)", () => {
    const mutableApi = api as unknown as { postFrontendLog?: PostFrontendLogFn }
    const original = mutableApi.postFrontendLog
    mutableApi.postFrontendLog = undefined
    try {
      expect(() => notifyError("Save failed")).not.toThrow()
      expect(toastError).toHaveBeenCalledWith("Save failed")
    } finally {
      mutableApi.postFrontendLog = original
    }
  })
})
