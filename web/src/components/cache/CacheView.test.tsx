import { describe, expect, it } from "vitest"

import { safeInt } from "./safe-int"

// safeInt backs the numeric knob inputs (thinThreshold, refreshAheadPct). The
// bug it guards: Number("") is 0, not NaN, so clearing a field would commit 0 —
// silently disabling a knob (e.g. refreshAheadPct: 0 turns refresh-ahead off).
describe("safeInt", () => {
  it("keeps the previous value for an empty or whitespace-only field", () => {
    // The regression: without the trim guard these each return 0.
    expect(safeInt("", 25)).toBe(25)
    expect(safeInt("   ", 25)).toBe(25)
  })

  it("keeps the previous value for a partial/non-numeric entry", () => {
    expect(safeInt("-", 25)).toBe(25)
    expect(safeInt("abc", 25)).toBe(25)
  })

  it("parses a valid number, including an explicit zero", () => {
    expect(safeInt("0", 25)).toBe(0)
    expect(safeInt("7", 25)).toBe(7)
    expect(safeInt("100", 25)).toBe(100)
  })
})
