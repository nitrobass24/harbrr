import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { fireEvent, render, screen, waitFor } from "@testing-library/react"
import { afterEach, describe, expect, it, vi } from "vitest"
import type { ReactNode } from "react"
import { BackupSection } from "./BackupSection"

function wrap(children: ReactNode) {
  return (
    <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      {children}
    </QueryClientProvider>
  )
}

function json(body: unknown, status: number, headers?: Record<string, string>): Response {
  return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json", ...headers } })
}

function setPassphraseFile(passphrase: string) {
  fireEvent.change(screen.getByLabelText("Passphrase", { selector: "#import-passphrase" }), { target: { value: passphrase } })
  const file = new File([JSON.stringify({ schemaVersion: 1, payload: "sealed" })], "backup.json", { type: "application/json" })
  const input = screen.getByLabelText("Backup file")
  fireEvent.change(input, { target: { files: [file] } })
}

describe("BackupSection", () => {
  afterEach(() => vi.unstubAllGlobals())

  it("renders export and import blocks", () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(json({}, 200)))
    render(wrap(<BackupSection />))
    expect(screen.getByRole("button", { name: "Export backup" })).toBeTruthy()
    expect(screen.getByRole("button", { name: "Import backup" })).toBeTruthy()
    expect(screen.getByText(/never stored/i)).toBeTruthy()
  })

  it("export posts the passphrase and triggers a download", async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      json({ schemaVersion: 1, payload: "sealed" }, 200, { "Content-Disposition": "attachment; filename=\"harbrr-backup-2026-07-15.json\"" })
    )
    vi.stubGlobal("fetch", fetchMock)
    URL.createObjectURL = vi.fn(() => "blob:mock")
    URL.revokeObjectURL = vi.fn()
    const clickSpy = vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(() => {})

    render(wrap(<BackupSection />))
    fireEvent.change(screen.getByLabelText("Passphrase", { selector: "#export-passphrase" }), { target: { value: "sekrit" } })
    fireEvent.change(screen.getByLabelText("Confirm passphrase"), { target: { value: "sekrit" } })
    fireEvent.click(screen.getByRole("button", { name: "Export backup" }))

    await waitFor(() => expect(clickSpy).toHaveBeenCalled())

    const call = fetchMock.mock.calls.find((args: unknown[]) => (args[0] as Request).url.endsWith("/api/export"))
    expect(call).toBeTruthy()
    const body: unknown = await (call![0] as Request).clone().json()
    expect(body).toEqual({ passphrase: "sekrit" })
  })

  it("409 opens the force-confirmation dialog, and confirming retries with force", async () => {
    let importCalls = 0
    const fetchMock = vi.fn().mockImplementation((request: Request) => {
      if (request.url.endsWith("/api/import")) {
        importCalls += 1
        return Promise.resolve(importCalls === 1 ? json({ code: "conflict", error: "not empty" }, 409) : new Response(null, { status: 204 }))
      }
      return Promise.resolve(json({}, 200))
    })
    vi.stubGlobal("fetch", fetchMock)
    const reload = vi.fn()
    vi.stubGlobal("location", { ...window.location, reload })

    render(wrap(<BackupSection />))
    setPassphraseFile("sekrit")
    fireEvent.click(screen.getByRole("button", { name: "Import backup" }))

    expect(await screen.findByText("Replace everything?")).toBeTruthy()
    fireEvent.click(screen.getByRole("button", { name: "Replace everything" }))

    await waitFor(() => expect(importCalls).toBe(2))
    await waitFor(() => expect(reload).toHaveBeenCalled())
  })

  it("400 (wrong passphrase) shows an inline error", async () => {
    vi.stubGlobal("fetch", vi.fn().mockImplementation((request: Request) => {
      if (request.url.endsWith("/api/import")) return Promise.resolve(json({ code: "invalid_argument", error: "wrong passphrase or corrupted bundle" }, 400))
      return Promise.resolve(json({}, 200))
    }))

    render(wrap(<BackupSection />))
    setPassphraseFile("wrong")
    fireEvent.click(screen.getByRole("button", { name: "Import backup" }))

    expect(await screen.findByText("wrong passphrase or corrupted bundle")).toBeTruthy()
  })

  it("import success notifies and reloads", async () => {
    vi.stubGlobal("fetch", vi.fn().mockImplementation((request: Request) => {
      if (request.url.endsWith("/api/import")) return Promise.resolve(new Response(null, { status: 204 }))
      return Promise.resolve(json({}, 200))
    }))
    const reload = vi.fn()
    vi.stubGlobal("location", { ...window.location, reload })

    render(wrap(<BackupSection />))
    setPassphraseFile("sekrit")
    fireEvent.click(screen.getByRole("button", { name: "Import backup" }))

    await waitFor(() => expect(reload).toHaveBeenCalled())
  })
})
