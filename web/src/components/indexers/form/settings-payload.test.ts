import { describe, expect, it } from "vitest"
import { REDACTED } from "@/types/api"
import type { Setting, SettingField } from "@/types/api"
import { defaultValues, isInfoField, settingsPayload } from "./settings-payload"

const SCHEMA: SettingField[] = [
  { name: "username", label: "Username", type: "text", secret: false },
  { name: "apikey", label: "API Key", type: "password", secret: true },
  { name: "freeleech", label: "Freeleech only", type: "checkbox", secret: false, default: "false" },
  { name: "sort", label: "Sort", type: "select", secret: false, options: { created: "created", seeders: "seeders" }, default: "created" },
  { name: "cats", label: "Categories", type: "multi-select", secret: false, options: { "1": "Movies", "2": "TV" } },
  { name: "note", label: "About this tracker", type: "info", secret: false, default: "Login uses the API key." },
]

describe("defaultValues", () => {
  it("seeds schema defaults and skips info fields", () => {
    const v = defaultValues(SCHEMA)
    expect(v).toEqual({ username: "", apikey: "", freeleech: "false", sort: "created", cats: "" })
    expect("note" in v).toBe(false)
  })

  it("overlays stored settings — secrets arrive as the sentinel and stay put", () => {
    const stored: Setting[] = [
      { name: "username", value: "alice", secret: false },
      { name: "apikey", value: REDACTED, secret: true },
    ]
    const v = defaultValues(SCHEMA, stored)
    expect(v.username).toBe("alice")
    expect(v.apikey).toBe(REDACTED)
  })
})

describe("settingsPayload", () => {
  it("create: drops empties and any sentinel value", () => {
    const payload = settingsPayload({ username: "alice", apikey: "", cats: "", stray: REDACTED }, "create")
    expect(payload).toEqual({ username: "alice" })
  })

  it("edit: sends untouched secrets back as the sentinel (keep-stored contract)", () => {
    const payload = settingsPayload({ username: "alice", apikey: REDACTED }, "edit")
    expect(payload.apikey).toBe(REDACTED)
  })

  it("edit: a rotated secret goes up as the new plaintext", () => {
    const payload = settingsPayload({ apikey: "fresh-secret" }, "edit")
    expect(payload.apikey).toBe("fresh-secret")
  })

  it("edit: keeps empty values so a field can be cleared", () => {
    const payload = settingsPayload({ username: "" }, "edit")
    expect(payload.username).toBe("")
  })
})

describe("isInfoField", () => {
  it("matches every info* variant", () => {
    for (const t of ["info", "info_cookie", "info_flaresolverr"]) {
      expect(isInfoField({ name: "x", type: t, secret: false })).toBe(true)
    }
    expect(isInfoField({ name: "x", type: "text", secret: false })).toBe(false)
  })
})
