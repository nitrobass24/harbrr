import { fireEvent, render, screen } from "@testing-library/react"
import { describe, expect, it, vi } from "vitest"
import type { SettingField } from "@/lib/api"
import { SettingFieldInput } from "./SettingFieldInput"

function renderField(field: SettingField, value = "") {
  const onChange = vi.fn<(v: string) => void>()
  render(<SettingFieldInput field={field} value={value} onChange={onChange} />)
  return onChange
}

describe("SettingFieldInput", () => {
  it("text renders a plain input", () => {
    const onChange = renderField({ name: "username", label: "Username", type: "text", secret: false })
    const input = screen.getByLabelText("Username")
    expect(input.getAttribute("type")).toBe("text")
    fireEvent.change(input, { target: { value: "alice" } })
    expect(onChange).toHaveBeenCalledWith("alice")
  })

  it("password and secret fields render masked", () => {
    renderField({ name: "apikey", label: "API Key", type: "password", secret: true })
    expect(screen.getByLabelText("API Key").getAttribute("type")).toBe("password")
  })

  it("a secret text field is masked too", () => {
    renderField({ name: "cookie", label: "Cookie", type: "text", secret: true })
    expect(screen.getByLabelText("Cookie").getAttribute("type")).toBe("password")
  })

  it("checkbox checking writes \"true\"", () => {
    const onChange = renderField({ name: "fl", label: "Freeleech", type: "checkbox", secret: false }, "")
    fireEvent.click(screen.getByLabelText("Freeleech"))
    expect(onChange).toHaveBeenCalledWith("true")
  })

  // autobrr/harbrr#273: unchecking must clear the key to "", not write the literal
  // "false" — a stored "false" is non-empty and reads as CHECKED downstream.
  it("checkbox unchecking clears to \"\" (autobrr/harbrr#273)", () => {
    const onChange = renderField({ name: "fl", label: "Freeleech", type: "checkbox", secret: false }, "true")
    fireEvent.click(screen.getByLabelText("Freeleech"))
    expect(onChange).toHaveBeenCalledWith("")
  })

  it("select renders every option and reports the key", () => {
    const onChange = renderField(
      { name: "sort", label: "Sort", type: "select", secret: false, options: { created: "Created", seeders: "Seeders" }, default: "created" },
      "created"
    )
    const select = screen.getByLabelText("Sort")
    expect(screen.getByRole("option", { name: "Created" })).toBeTruthy()
    expect(screen.getByRole("option", { name: "Seeders" })).toBeTruthy()
    fireEvent.change(select, { target: { value: "seeders" } })
    expect(onChange).toHaveBeenCalledWith("seeders")
  })

  it("multi-select toggles comma-joined keys", () => {
    const onChange = renderField(
      { name: "cats", label: "Categories", type: "multi-select", secret: false, options: { "1": "Movies", "2": "TV" } },
      "1"
    )
    fireEvent.click(screen.getByLabelText("TV"))
    expect(onChange).toHaveBeenCalledWith("1,2")
  })

  it("info fields render documentation, not an input", () => {
    renderField({ name: "note", label: "Note", type: "info", secret: false, default: "Login uses the API key." })
    expect(screen.getByText(/Login uses the API key/)).toBeTruthy()
    expect(screen.queryByRole("textbox")).toBeNull()
    expect(screen.queryByRole("checkbox")).toBeNull()
  })
})
