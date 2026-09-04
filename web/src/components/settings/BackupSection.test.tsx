import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { fireEvent, render, screen, waitFor } from "@testing-library/react"
import { afterEach, describe, expect, it, vi } from "vitest"
import type { ReactNode } from "react"
import { stubApi } from "@/test/stubApi"
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

// submitImport fires the form's submit event directly. The file input carries
// `required`, and clicking a submit button (or requestSubmit) runs native
// constraint validation, which jsdom fails because it doesn't treat a
// programmatically-set FileList as satisfying `required` — a real browser does.
// Dispatching submit exercises onSubmit, which is what these tests assert.
function submitImport() {
  const form = screen.getByRole("button", { name: "Import backup" }).closest("form")!
  fireEvent.submit(form)
}

describe("BackupSection", () => {
  // The reload tests stub `location`; unstub so it never leaks between tests.
  afterEach(() => vi.unstubAllGlobals())

  it("renders export and import blocks", () => {
    stubApi({})
    render(wrap(<BackupSection />))
    expect(screen.getByRole("button", { name: "Export backup" })).toBeTruthy()
    expect(screen.getByRole("button", { name: "Import backup" })).toBeTruthy()
    expect(screen.getByText(/never stored/i)).toBeTruthy()
  })

  it("export posts the passphrase and triggers a download", async () => {
    const api = stubApi({
      "POST /api/export": () =>
        json({ schemaVersion: 1, payload: "sealed" }, 200, { "Content-Disposition": "attachment; filename=\"harbrr-backup-2026-07-15.json\"" }),
    })
    URL.createObjectURL = vi.fn(() => "blob:mock")
    URL.revokeObjectURL = vi.fn()
    const clickSpy = vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(() => {})

    render(wrap(<BackupSection />))
    fireEvent.change(screen.getByLabelText("Passphrase", { selector: "#export-passphrase" }), { target: { value: "sekrit" } })
    fireEvent.change(screen.getByLabelText("Confirm passphrase"), { target: { value: "sekrit" } })
    fireEvent.click(screen.getByRole("button", { name: "Export backup" }))

    await waitFor(() => expect(clickSpy).toHaveBeenCalled())

    const call = api.calls("POST /api/export")[0]
    expect(call).toBeTruthy()
    const body: unknown = await call.clone().json()
    expect(body).toEqual({ passphrase: "sekrit" })
  })

  it("409 opens the force-confirmation dialog, and confirming retries with force", async () => {
    let importCalls = 0
    stubApi({
      "POST /api/import": () => {
        importCalls += 1
        return importCalls === 1 ? json({ code: "conflict", error: "not empty" }, 409) : new Response(null, { status: 204 })
      },
    })
    const reload = vi.fn()
    vi.stubGlobal("location", { ...window.location, reload })

    render(wrap(<BackupSection />))
    setPassphraseFile("sekrit")
    submitImport()

    expect(await screen.findByText("Replace everything?")).toBeTruthy()
    fireEvent.click(screen.getByRole("button", { name: "Replace everything" }))

    await waitFor(() => expect(importCalls).toBe(2))
    await waitFor(() => expect(reload).toHaveBeenCalled())
  })

  it("400 (wrong passphrase) shows an inline error", async () => {
    stubApi({
      "POST /api/import": () => json({ code: "invalid_argument", error: "wrong passphrase or corrupted bundle" }, 400),
    })

    render(wrap(<BackupSection />))
    setPassphraseFile("wrong")
    submitImport()

    expect(await screen.findByText("wrong passphrase or corrupted bundle")).toBeTruthy()
  })

  it("import success notifies and reloads", async () => {
    stubApi({ "POST /api/import": () => new Response(null, { status: 204 }) })
    const reload = vi.fn()
    vi.stubGlobal("location", { ...window.location, reload })

    render(wrap(<BackupSection />))
    setPassphraseFile("sekrit")
    submitImport()

    await waitFor(() => expect(reload).toHaveBeenCalled())
  })
})
