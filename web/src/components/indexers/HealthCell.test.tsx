import { render, screen } from "@testing-library/react"
import { describe, expect, it } from "vitest"
import type { IndexerStatus } from "@/lib/api"
import { HealthCell, healthDetail } from "./HealthCell"

const MONTH_AGO = new Date(Date.now() - 31 * 86_400_000).toISOString()

// autobrr/harbrr#473: the cell captions only what it can assert is current. One fix in
// HealthCell covers every render site (table, mobile cards, dashboard strip).
describe("HealthCell", () => {
  it("shows no event caption for a never-tested status carrying a stale failure", () => {
    const status: IndexerStatus = {
      slug: "nzbindex",
      status: "unknown",
      events: [{ kind: "parse_error", detail: "boom", occurred_at: MONTH_AGO }],
    }
    render(<HealthCell status={status} />)

    expect(screen.getByText("Never tested")).toBeTruthy()
    expect(screen.queryByText(/response didn't parse/)).toBeNull()
    // The expanded detail is untouched: still the honest-absence line, never an error.
    expect(healthDetail(status)).toEqual([
      { label: "Why", value: "Nothing has queried or tested it yet. Not an error." },
    ])
  })

  it("still captions a failing status with its newest event", () => {
    render(
      <HealthCell
        status={{
          slug: "tt",
          status: "failing",
          events: [{ kind: "auth_failure", detail: "401", occurred_at: new Date(Date.now() - 120_000).toISOString() }],
        }}
      />
    )

    expect(screen.getByText("Failing")).toBeTruthy()
    expect(screen.getByText(/login failed 2m ago/)).toBeTruthy()
  })
})
