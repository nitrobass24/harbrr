import { render, screen } from "@testing-library/react"
import { describe, expect, it } from "vitest"
import { BudgetMeter, countdown } from "./BudgetMeter"
import type { IndexerBudget } from "@/lib/api"

// An hour out, so "Resets in 1h" is stable regardless of when the suite runs.
const periodEnd = new Date(Date.now() + 61 * 60_000).toISOString()

function budget(over: Partial<IndexerBudget> = {}): IndexerBudget {
  return {
    unit: "day",
    periodEnd,
    query: { used: 0, limit: 0, detected: false, learned: false },
    grab: { used: 0, limit: 0, detected: false, learned: false },
    ...over,
  }
}

describe("BudgetMeter", () => {
  it("meters a configured cap with usage and the reset boundary", () => {
    render(<BudgetMeter budget={budget({ query: { used: 1500, limit: 2000, detected: false, learned: false } })} />)
    expect(screen.getByText("1,500 / 2,000")).toBeTruthy()
    expect(screen.getByText(/Resets in 1h/)).toBeTruthy()
    // The uncapped grab kind is not drawn at all.
    expect(screen.queryByText("Grabs")).toBeNull()
    expect(screen.queryByText("learned cap")).toBeNull()
    expect(screen.queryByText("detected cap")).toBeNull()
  })

  it("marks a detected cap distinctly from one the operator typed", () => {
    render(<BudgetMeter budget={budget({ query: { used: 12, limit: 2000, detected: true, learned: false } })} />)
    expect(screen.getByText("detected cap")).toBeTruthy()
    expect(screen.getByText("12 / 2,000")).toBeTruthy()
    expect(screen.queryByText("learned cap")).toBeNull()
  })

  it("marks a learned cap distinctly from a configured one", () => {
    render(<BudgetMeter budget={budget({ query: { used: 40, limit: 0, detected: false, learned: true } })} />)
    expect(screen.getByText("learned cap")).toBeTruthy()
    expect(screen.getByText(/Tracker reported its quota spent/)).toBeTruthy()
  })

  it("renders a spent configured budget as a held-back guard, not a failure", () => {
    render(<BudgetMeter budget={budget({ query: { used: 2000, limit: 2000, detected: false, learned: false } })} />)
    const note = screen.getByText(/Budget spent for this period/)
    expect(note.className).toContain("text-warn")
    expect(note.className).not.toContain("text-bad")
  })

  it("stays quiet for an indexer with no cap of any kind", () => {
    render(<BudgetMeter budget={budget()} />)
    expect(screen.getByText(/No request budget set/)).toBeTruthy()
    expect(screen.queryByText("Searches")).toBeNull()
  })

  it("renders nothing while the stats are still loading", () => {
    const { container } = render(<BudgetMeter budget={undefined} />)
    expect(container.textContent).toBe("")
  })
})

describe("countdown", () => {
  it("renders whole hours, minutes, and a floor for anything shorter", () => {
    const now = new Date("2026-07-17T12:00:00Z")
    expect(countdown("2026-07-18T00:00:00Z", now)).toBe("12h")
    expect(countdown("2026-07-17T12:42:00Z", now)).toBe("42m")
    expect(countdown("2026-07-17T12:00:30Z", now)).toBe("under a minute")
  })
})
