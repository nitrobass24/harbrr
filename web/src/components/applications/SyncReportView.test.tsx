import { render, screen } from "@testing-library/react"
import { describe, expect, it } from "vitest"
import type { SyncReport } from "@/lib/api"
import { SyncReportView } from "./SyncReportView"

const PARTIAL: SyncReport = {
  status: "partial",
  results: [
    { slug: "torrentleech", action: "created" },
    { slug: "iptorrents", action: "updated" },
    { slug: "alpharatio", action: "noop" },
    { slug: "stale-one", action: "deleted" },
    { slug: "rutor", action: "failed", error: "app rejected the payload (400)" },
  ],
}

describe("SyncReportView", () => {
  it("renders the overall status and every per-indexer action", () => {
    render(<SyncReportView report={PARTIAL} />)

    expect(screen.getByText("partial")).toBeTruthy()
    for (const slug of ["torrentleech", "iptorrents", "alpharatio", "stale-one", "rutor"]) {
      expect(screen.getByText(slug)).toBeTruthy()
    }
    for (const action of ["created", "updated", "noop", "deleted", "failed"]) {
      expect(screen.getByText(action)).toBeTruthy()
    }
    expect(screen.getByText("app rejected the payload (400)")).toBeTruthy()
  })

  it("renders an ok report without errors", () => {
    render(<SyncReportView report={{ status: "ok", results: [{ slug: "tl", action: "noop" }] }} />)
    expect(screen.getByText("ok")).toBeTruthy()
    expect(screen.queryByText(/rejected/)).toBeNull()
  })
})
