import { useState } from "react"
import { fireEvent, render, screen, waitFor } from "@testing-library/react"
import { afterEach, describe, expect, it, vi } from "vitest"
import { notifyError, notifySuccess } from "@/lib/notify"
import { ResourceSection } from "./resource-section"
import type { UseQueryResult } from "@tanstack/react-query"

vi.mock("@/lib/notify", () => ({ notifyError: vi.fn(), notifySuccess: vi.fn() }))

interface Widget { id: number, name: string }

const WIDGETS: Widget[] = [{ id: 1, name: "alpha" }, { id: 2, name: "beta" }]

// The shell consumes a UseQueryResult as a plain prop, so a stub object stands in
// for a live query — no QueryClient needed here (the section tests cover that wiring).
function query(overrides: Partial<UseQueryResult<Widget[]>> = {}): UseQueryResult<Widget[]> {
  return { isPending: false, isError: false, data: WIDGETS, ...overrides } as UseQueryResult<Widget[]>
}

// Seeds local state from the target once — if the shell fails to remount the form
// per target, the stale seed shows and the remount assertions below catch it.
function SeededForm({ target }: { target: Widget | null }) {
  const [seed] = useState(target?.name ?? "new")
  return <p data-testid="form-target">{seed}</p>
}

function renderShell({ q = query(), onDelete }: {
  q?: UseQueryResult<Widget[]>
  onDelete?: (w: Widget) => Promise<unknown>
} = {}) {
  return render(
    <ResourceSection<Widget>
      title="Widgets"
      addLabel="Add widget"
      query={q}
      empty="No widgets yet."
      row={(w, actions) => (
        <>
          <span>{w.name}</span>
          <button onClick={actions.edit}>Edit {w.name}</button>
          {actions.remove && <button onClick={actions.remove}>Delete {w.name}</button>}
        </>
      )}
      form={(target) => <SeededForm target={target} />}
      onDelete={onDelete}
    />
  )
}

async function closeDialog() {
  fireEvent.keyDown(document.body, { key: "Escape" })
  await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull())
}

describe("ResourceSection", () => {
  afterEach(() => vi.clearAllMocks())

  it("pending: renders the loading placeholder, not the empty card", () => {
    const { container } = renderShell({ q: query({ isPending: true, data: undefined }) })

    expect(container.querySelector(".animate-pulse")).toBeTruthy()
    expect(screen.queryByText("No widgets yet.")).toBeNull()
  })

  it("error: a failed list query shows LoadError, never an empty card", () => {
    renderShell({ q: query({ isError: true, data: undefined }) })

    expect(screen.getByRole("alert").textContent).toContain("Loading widgets failed")
    expect(screen.queryByText("No widgets yet.")).toBeNull()
  })

  it("empty: shows the empty message for a successful empty list", () => {
    renderShell({ q: query({ data: [] }) })

    expect(screen.getByText("No widgets yet.")).toBeTruthy()
  })

  it("add: opens the dialog with a null target", async () => {
    renderShell()

    fireEvent.click(screen.getByRole("button", { name: /Add widget/ }))

    expect((await screen.findByTestId("form-target")).textContent).toBe("new")
  })

  it("edit: passes the row's item and remounts the form per target", async () => {
    renderShell()

    fireEvent.click(screen.getByRole("button", { name: "Edit beta" }))
    expect((await screen.findByTestId("form-target")).textContent).toBe("beta")

    await closeDialog()
    fireEvent.click(screen.getByRole("button", { name: "Edit alpha" }))
    // A stale (non-remounted) form would still show its "beta" seed.
    expect((await screen.findByTestId("form-target")).textContent).toBe("alpha")
  })

  it("delete: calls onDelete and toasts the success", async () => {
    const onDelete = vi.fn().mockResolvedValue(undefined)
    renderShell({ onDelete })

    fireEvent.click(screen.getByRole("button", { name: "Delete beta" }))

    await waitFor(() => expect(notifySuccess).toHaveBeenCalledWith("beta deleted"))
    expect(onDelete).toHaveBeenCalledWith(WIDGETS[1])
    expect(notifyError).not.toHaveBeenCalled()
  })

  it("delete: a failing onDelete toasts the error", async () => {
    const boom = new Error("nope")
    renderShell({ onDelete: vi.fn().mockRejectedValue(boom) })

    fireEvent.click(screen.getByRole("button", { name: "Delete beta" }))

    await waitFor(() => expect(notifyError).toHaveBeenCalledWith("Deleting beta failed", boom))
    expect(notifySuccess).not.toHaveBeenCalled()
  })

  it("no onDelete: rows get no remove action", () => {
    renderShell()

    expect(screen.queryByRole("button", { name: "Delete beta" })).toBeNull()
  })
})
