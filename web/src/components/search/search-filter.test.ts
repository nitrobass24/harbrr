import { describe, expect, it } from "vitest"
import { filterGroups, filterRows } from "./search-filter"
import type { ResultGroup } from "./search-group"
import type { SearchRow } from "./search-sort"

const row = (indexer: string, title: string, categories: number[]): SearchRow => ({
  indexer,
  release: { title, categories },
})

const ROWS: SearchRow[] = [
  row("demotracker", "Big Buck Bunny 2160p x265-GROUP", [2000]),
  row("demopublic", "Sintel S01E02 1080p x264", [5000]),
  row("demotracker", "Tears of Steel 1080p x265", [2000]),
]

const CATS = new Map([[2000, "Movies"], [5000, "TV"]])

const titles = (rows: SearchRow[] | null) => (rows ?? []).map((r) => r.release.title)

describe("filterRows", () => {
  it("returns the identical array for empty or whitespace-only input", () => {
    expect(filterRows(ROWS, "", CATS)).toBe(ROWS)
    expect(filterRows(ROWS, "   ", CATS)).toBe(ROWS)
  })

  const cases: { name: string, input: string, want: string[] }[] = [
    { name: "substring over title, case-insensitive", input: "SINTEL", want: ["Sintel S01E02 1080p x264"] },
    {
      name: "substring over the indexer slug",
      input: "demotracker",
      want: ["Big Buck Bunny 2160p x265-GROUP", "Tears of Steel 1080p x265"],
    },
    { name: "substring over the category name", input: "tv", want: ["Sintel S01E02 1080p x264"] },
    { name: "multiple terms AND together", input: "x265 tears", want: ["Tears of Steel 1080p x265"] },
    {
      name: "a leading - excludes",
      input: "x265 -bunny",
      want: ["Tears of Steel 1080p x265"],
    },
    { name: "a leading ! excludes too", input: "!x265", want: ["Sintel S01E02 1080p x264"] },
    {
      name: "/regex/ mode",
      input: String.raw`/S\d\dE\d\d/`,
      want: ["Sintel S01E02 1080p x264"],
    },
    { name: "/regex/ is case-insensitive", input: "/BUNNY/", want: ["Big Buck Bunny 2160p x265-GROUP"] },
    {
      name: "a negated regex excludes",
      input: String.raw`-/\d160p/`,
      want: ["Sintel S01E02 1080p x264", "Tears of Steel 1080p x265"],
    },
    {
      name: "a regex term keeps its spaces",
      input: "/tears of/ x265",
      want: ["Tears of Steel 1080p x265"],
    },
    { name: "a lone - is not yet a filter", input: "-", want: titles(ROWS) },
    { name: "no match yields an empty result", input: "nothingmatchesthis", want: [] },
  ]

  for (const c of cases) {
    it(c.name, () => {
      expect(titles(filterRows(ROWS, c.input, CATS))).toEqual(c.want)
    })
  }

  const invalid = ["/unterminated", "/", "/[/", "x265 /(/", "-/*/"]
  for (const input of invalid) {
    it(`signals invalid (null) for ${JSON.stringify(input)} rather than blanking results`, () => {
      expect(filterRows(ROWS, input, CATS)).toBeNull()
    })
  }

  it("falls back to the raw category id when the name is unknown", () => {
    expect(titles(filterRows(ROWS, "5000", new Map()))).toEqual(["Sintel S01E02 1080p x264"])
  })
})

// One release carried by two trackers plus one carried by a third.
const GROUPS: ResultGroup[] = [
  { key: "h:abc", members: [row("demotracker", "Tears of Steel 1080p", [2000]), row("demopublic", "Tears of Steel 1080p", [2000])] },
  { key: "h:def", members: [row("demopublic", "Sintel S01E02 1080p x264", [5000])] },
]

const keys = (groups: ResultGroup[] | null) => (groups ?? []).map((g) => g.key)

describe("filterGroups (autobrr/harbrr#398)", () => {
  it("returns the identical array for empty input", () => {
    expect(filterGroups(GROUPS, "", CATS)).toBe(GROUPS)
  })

  it("keeps a group whole when ANY member matches — never half-collapsing its sources", () => {
    const matched = filterGroups(GROUPS, "demotracker", CATS)
    expect(keys(matched)).toEqual(["h:abc"])
    // The non-matching member of a matching group stays: which trackers carry the
    // release is the point of the group.
    expect(matched![0].members.map((m) => m.indexer)).toEqual(["demotracker", "demopublic"])
  })

  it("drops a group only when no member matches", () => {
    expect(keys(filterGroups(GROUPS, "sintel", CATS))).toEqual(["h:def"])
    expect(keys(filterGroups(GROUPS, "nothingmatchesthis", CATS))).toEqual([])
  })

  it("applies negation and regex terms the same way filterRows does", () => {
    expect(keys(filterGroups(GROUPS, "-tears", CATS))).toEqual(["h:def"])
    expect(keys(filterGroups(GROUPS, String.raw`/S\d\dE\d\d/`, CATS))).toEqual(["h:def"])
  })

  it("signals invalid (null) rather than blanking results", () => {
    expect(filterGroups(GROUPS, "/unterminated", CATS)).toBeNull()
  })
})
