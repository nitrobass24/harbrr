import { describe, expect, it } from "vitest"
import { filterGroups } from "./search-filter"
import type { ResultGroup } from "./search-group"
import type { SearchRow } from "./search-sort"

const row = (indexer: string, title: string, categories: number[]): SearchRow => ({
  indexer,
  release: { title, categories },
})

const CATS = new Map([[2000, "Movies"], [5000, "TV"]])

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

  it("applies negation and regex terms across members", () => {
    expect(keys(filterGroups(GROUPS, "-tears", CATS))).toEqual(["h:def"])
    expect(keys(filterGroups(GROUPS, String.raw`/S\d\dE\d\d/`, CATS))).toEqual(["h:def"])
  })

  it("matches the category name, not just the title and indexer", () => {
    expect(keys(filterGroups(GROUPS, "tv", CATS))).toEqual(["h:def"])
    expect(keys(filterGroups(GROUPS, "movies", CATS))).toEqual(["h:abc"])
  })

  it("falls back to the raw category id when the name is unknown", () => {
    expect(keys(filterGroups(GROUPS, "5000", new Map()))).toEqual(["h:def"])
  })

  it("keeps a regex term's internal spaces instead of splitting it in two", () => {
    // Split on whitespace, "/tears" and "of/" would both be invalid regexes and the
    // whole input would read as null rather than as a match.
    expect(keys(filterGroups(GROUPS, "/tears of/", CATS))).toEqual(["h:abc"])
  })

  it.each(["/unterminated", "/", "/[/", "x265 /(/", "-/*/"])(
    "signals invalid (null) for %j rather than blanking results",
    (input) => {
      expect(filterGroups(GROUPS, input, CATS)).toBeNull()
    })
})
