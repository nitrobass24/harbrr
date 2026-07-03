import { fireEvent, render, screen } from "@testing-library/react"
import { describe, expect, it, vi } from "vitest"
import { REDACTED } from "@/types/api"
import type { DefinitionDetail, InstanceDetail } from "@/types/api"
import { IndexerForm, type IndexerFormSubmit } from "./IndexerForm"

const DEFINITION: DefinitionDetail = {
  id: "testtracker",
  name: "Test Tracker",
  type: "private",
  settings: [
    { name: "username", label: "Username", type: "text", secret: false },
    { name: "apikey", label: "API Key", type: "password", secret: true },
  ],
  caps: { modes: { search: ["q"] } },
}

const EXISTING: InstanceDetail = {
  id: 1,
  slug: "tt",
  definitionId: "testtracker",
  name: "TT",
  enabled: true,
  createdAt: "2026-07-01T00:00:00Z",
  updatedAt: "2026-07-01T00:00:00Z",
  settings: [
    { name: "username", value: "alice", secret: false },
    { name: "apikey", value: REDACTED, secret: true },
  ],
}

describe("IndexerForm", () => {
  it("edit: PATCH payload preserves the sentinel for an untouched secret", () => {
    const onSubmit = vi.fn<(s: IndexerFormSubmit) => void>()
    render(<IndexerForm definition={DEFINITION} existing={EXISTING} pending={false} error={null} onSubmit={onSubmit} />)

    // The secret arrives prefilled with the sentinel in a masked input.
    const secret = screen.getByLabelText("API Key")
    expect((secret as HTMLInputElement).value).toBe(REDACTED)
    expect(secret.getAttribute("type")).toBe("password")

    // Touch a non-secret field only, then save.
    fireEvent.change(screen.getByLabelText("Username"), { target: { value: "bob" } })
    fireEvent.click(screen.getByRole("button", { name: "Save changes" }))

    const submit = onSubmit.mock.calls[0][0]
    expect(submit.mode).toBe("edit")
    expect(submit.body.settings?.apikey).toBe(REDACTED)
    expect(submit.body.settings?.username).toBe("bob")
  })

  it("edit: a rotated secret submits the new plaintext", () => {
    const onSubmit = vi.fn<(s: IndexerFormSubmit) => void>()
    render(<IndexerForm definition={DEFINITION} existing={EXISTING} pending={false} error={null} onSubmit={onSubmit} />)

    fireEvent.change(screen.getByLabelText("API Key"), { target: { value: "fresh-key" } })
    fireEvent.click(screen.getByRole("button", { name: "Save changes" }))

    expect(onSubmit.mock.calls[0][0].body.settings?.apikey).toBe("fresh-key")
  })

  it("create: empty fields are stripped and the definition seeds slug + name", () => {
    const onSubmit = vi.fn<(s: IndexerFormSubmit) => void>()
    render(<IndexerForm definition={DEFINITION} pending={false} error={null} onSubmit={onSubmit} />)

    fireEvent.change(screen.getByLabelText("API Key"), { target: { value: "k123" } })
    fireEvent.click(screen.getByRole("button", { name: "Add indexer" }))

    const submit = onSubmit.mock.calls[0][0]
    expect(submit.mode).toBe("create")
    if (submit.mode === "create") {
      expect(submit.body.definitionId).toBe("testtracker")
      expect(submit.body.slug).toBe("testtracker")
      expect(submit.body.settings).toEqual({ apikey: "k123" })
    }
  })

  it("slug is locked in edit mode", () => {
    render(<IndexerForm definition={DEFINITION} existing={EXISTING} pending={false} error={null} onSubmit={vi.fn()} />)
    expect(screen.getByLabelText<HTMLInputElement>("Slug").disabled).toBe(true)
  })
})
