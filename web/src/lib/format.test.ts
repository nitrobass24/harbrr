import { describe, expect, it } from "vitest"
import { formatSize, hostname, kindLabel, relativeTime, syncStatusClass } from "./format"

describe("formatSize", () => {
  const cases: { bytes: number | undefined, want: string }[] = [
    { bytes: undefined, want: "—" },
    { bytes: 0, want: "—" },
    { bytes: 512, want: "512 B" },
    { bytes: 1024, want: "1.0 KiB" },
    { bytes: 2_684_354_560, want: "2.5 GiB" },
    { bytes: 1_649_267_441_664, want: "1.5 TiB" },
  ]
  for (const c of cases) {
    it(`${c.bytes} -> ${c.want}`, () => expect(formatSize(c.bytes)).toBe(c.want))
  }
})

describe("relativeTime", () => {
  const now = new Date("2026-07-03T12:00:00Z")
  const cases: { iso: string, want: string }[] = [
    { iso: "2026-07-03T11:59:40Z", want: "just now" },
    { iso: "2026-07-03T11:58:00Z", want: "2m ago" },
    { iso: "2026-07-03T09:00:00Z", want: "3h ago" },
    { iso: "2026-06-30T12:00:00Z", want: "3d ago" },
    { iso: "not-a-date", want: "" },
  ]
  for (const c of cases) {
    it(`${c.iso} -> "${c.want}"`, () => expect(relativeTime(c.iso, now)).toBe(c.want))
  }
})

describe("syncStatusClass", () => {
  const cases: { status: string | undefined, want: string }[] = [
    { status: "ok", want: "text-ok" },
    { status: "partial", want: "text-warn" },
    { status: "error", want: "text-bad" },
    { status: "skipped", want: "text-faint" },
    { status: undefined, want: "text-faint" },
    { status: "nonsense", want: "text-faint" },
    // Object.prototype keys must not resolve to an inherited member.
    { status: "toString", want: "text-faint" },
    { status: "constructor", want: "text-faint" },
    { status: "__proto__", want: "text-faint" },
  ]
  for (const c of cases) {
    it(`${c.status} -> ${c.want}`, () => expect(syncStatusClass(c.status)).toBe(c.want))
  }
})

describe("kindLabel", () => {
  it("maps known slugs and echoes anything else, including prototype keys", () => {
    expect(kindLabel("qbittorrent")).toBe("qBittorrent")
    expect(kindLabel("unknown-kind")).toBe("unknown-kind")
    expect(kindLabel("toString")).toBe("toString")
  })
})

describe("hostname", () => {
  it("extracts the host and falls back to the raw string", () => {
    expect(hostname("https://www.torrentleech.org/x")).toBe("www.torrentleech.org")
    expect(hostname("not a url")).toBe("not a url")
    expect(hostname(undefined)).toBe("")
  })
})
