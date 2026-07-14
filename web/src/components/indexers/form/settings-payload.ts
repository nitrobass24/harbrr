import { REDACTED } from "@/lib/api"
import type { Setting, SettingField } from "@/lib/api"

// isInfoField: info* rows are rendered documentation, never inputs, and never
// submitted (text | password | checkbox | select | multi-select are inputs).
export function isInfoField(field: SettingField): boolean {
  return field.type.startsWith("info")
}

// defaultValues seeds the form: stored settings (edit — secrets arrive as the
// <redacted> sentinel and stay prefilled) over schema defaults (create).
export function defaultValues(fields: SettingField[], stored?: Setting[]): Record<string, string> {
  const values: Record<string, string> = {}
  for (const f of fields) {
    if (isInfoField(f)) continue
    values[f.name] = f.default ?? (f.type === "checkbox" ? "false" : "")
  }
  for (const s of stored ?? []) {
    values[s.name] = s.value
  }
  return values
}

// settingsPayload builds the settings map for the API from form values.
//
// The keep-stored contract (openapi.yaml UpdateIndexer): on PATCH, a secret the
// operator did not touch still holds the <redacted> sentinel and MUST be sent
// back verbatim — the server keeps the stored value. On POST there is no stored
// value, so sentinel values are stripped. Empty values are dropped on create
// (the engine applies its own defaults) but kept on edit (clearing a field).
export function settingsPayload(values: Record<string, string>, mode: "create" | "edit"): Record<string, string> {
  const payload: Record<string, string> = {}
  for (const [name, value] of Object.entries(values)) {
    if (mode === "create" && (value === "" || value === REDACTED)) continue
    payload[name] = value
  }
  return payload
}
