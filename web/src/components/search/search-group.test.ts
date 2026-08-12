import { describe, expect, it } from "vitest"
import { bestMember, groupRows, soloGroups, sortGroups } from "./search-group"
import type { SearchRow, Sort } from "./search-sort"
import { sortRows } from "./search-sort"

const GIB = 1_073_741_824

function row(indexer: string, release: Partial<SearchRow["release"]> & { title: string }, protocol: SearchRow["protocol"] = "torrent"): SearchRow {
  return { indexer, protocol, release: { size: GIB, ...release } }
}

const sources = (members: SearchRow[]) => members.map((m) => m.indexer)

describe("groupRows — the matcher", () => {
  it("groups on a shared infohash, case-insensitively, across trackers", () => {
    const groups = groupRows([
      row("alpha", { title: "Big Buck Bunny 2160p", infohash: "ABCDEF" }),
      row("beta", { title: "big.buck.bunny.2160p", infohash: "abcdef", size: 7 * GIB }),
    ])

    expect(groups).toHaveLength(1)
    expect(sources(groups[0].members)).toEqual(["alpha", "beta"])
  })

  it("groups on normalized title + exact size when neither side has an infohash", () => {
    const groups = groupRows([
      row("alpha", { title: "Big Buck Bunny 2160p x265-GROUP" }),
      row("beta", { title: "big.buck.bunny.2160p.x265-GROUP" }),
    ])

    expect(groups).toHaveLength(1)
    expect(sources(groups[0].members)).toEqual(["alpha", "beta"])
  })

  const splits: { name: string, rows: SearchRow[] }[] = [
    {
      name: "a size that differs by a single byte — a tolerance window is what would hide a release",
      rows: [
        row("alpha", { title: "Tears of Steel 1080p" }),
        row("beta", { title: "Tears of Steel 1080p", size: GIB + 1 }),
      ],
    },
    {
      name: "different infohashes, however alike the titles and sizes are",
      rows: [
        row("alpha", { title: "Tears of Steel 1080p", infohash: "aaa" }),
        row("beta", { title: "Tears of Steel 1080p", infohash: "bbb" }),
      ],
    },
    {
      name: "a title match with no size on either side — title alone never merges",
      rows: [
        row("alpha", { title: "Sintel 1080p", size: undefined }),
        row("beta", { title: "Sintel 1080p", size: undefined }),
      ],
    },
    {
      name: "a size of 0, which trackers serve for 'unknown'",
      rows: [
        row("alpha", { title: "Sintel 1080p", size: 0 }),
        row("beta", { title: "Sintel 1080p", size: 0 }),
      ],
    },
    {
      // A newznab feed can carry an infohash, so it is not a torrent-only tell.
      name: "a usenet and a torrent entry sharing an infohash",
      rows: [
        row("alpha", { title: "Sintel 1080p", infohash: "abc" }, "torrent"),
        row("beta", { title: "Sintel 1080p", infohash: "abc" }, "usenet"),
      ],
    },
    {
      name: "a shared infohash where one side's protocol is unresolved",
      rows: [
        row("alpha", { title: "Sintel 1080p", infohash: "abc" }, "torrent"),
        { indexer: "beta", release: { title: "Sintel 1080p", infohash: "abc", size: GIB } },
      ],
    },
    {
      name: "a usenet and a torrent entry, identical title and size",
      rows: [
        row("alpha", { title: "Sintel 1080p" }, "torrent"),
        row("beta", { title: "Sintel 1080p" }, "usenet"),
      ],
    },
    {
      name: "an unresolved protocol, which could be either",
      rows: [
        { indexer: "alpha", release: { title: "Sintel 1080p", size: GIB } },
        { indexer: "beta", release: { title: "Sintel 1080p", size: GIB } },
      ],
    },
    {
      name: "distinct releases sharing a title — quality, edition and group live in the title",
      rows: [
        row("alpha", { title: "Sintel 2010 1080p BluRay x264-GROUP" }),
        row("alpha", { title: "Sintel 2010 1080p Extended BluRay x264-GROUP" }),
        row("beta", { title: "Sintel 2010 2160p BluRay x265-OTHER" }),
      ],
    },
  ]

  for (const c of splits) {
    it(`never merges ${c.name}`, () => {
      const groups = groupRows(c.rows)
      expect(groups).toHaveLength(c.rows.length)
      expect(groups.every((g) => g.members.length === 1)).toBe(true)
    })
  }

  it("keeps first-seen order and never drops a row", () => {
    const rows = [
      row("alpha", { title: "A" }),
      row("beta", { title: "B" }),
      row("gamma", { title: "A" }),
    ]
    const groups = groupRows(rows)

    expect(groups.map((g) => g.members[0].release.title)).toEqual(["A", "B"])
    expect(groups.flatMap((g) => g.members)).toHaveLength(3)
    expect(sources(groups[0].members)).toEqual(["alpha", "gamma"])
  })

  it("gives every group a unique key, including ungroupable rows", () => {
    const groups = groupRows([
      row("alpha", { title: "Sintel", size: 0 }),
      row("alpha", { title: "Sintel", size: 0 }),
      row("beta", { title: "Bunny", infohash: "abc" }),
    ])
    expect(new Set(groups.map((g) => g.key)).size).toBe(groups.length)
  })
})

describe("soloGroups / sortGroups", () => {
  const ROWS = [
    row("alpha", { title: "Shared", infohash: "abc", seeders: 3, publishDate: "2026-01-03T00:00:00Z" }),
    row("beta", { title: "Solo", seeders: 5, size: 2 * GIB, publishDate: "2026-01-02T00:00:00Z" }),
    row("gamma", { title: "Shared", infohash: "ABC", seeders: 9, publishDate: "2026-01-01T00:00:00Z" }),
  ]

  it("grouping off reproduces the flat list exactly — same rows, same order", () => {
    for (const sort of [{ key: "seeders", dir: "desc" }, { key: "age", dir: "asc" }, { key: "title", dir: "asc" }] as Sort[]) {
      const flat = sortRows(ROWS, sort)
      const solo = soloGroups(flat)
      expect(solo.flatMap((g) => g.members)).toEqual(flat)
      expect(solo.every((g) => g.members.length === 1)).toBe(true)
      // …and it stays flat through a sort pass, which a singleton group cannot change.
      expect(sortGroups(solo, sort).flatMap((g) => g.members)).toEqual(flat)
    }
  })

  it("sorts a group on its best member, and renders that member", () => {
    const groups = groupRows(ROWS)
    const bySeeders = sortGroups(groups, { key: "seeders", dir: "desc" })

    // The 2-member group leads on its 9-seeder member even though its first member has 3.
    expect(bySeeders[0].members).toHaveLength(2)
    expect(bestMember(bySeeders[0], { key: "seeders", dir: "desc" }).indexer).toBe("gamma")
    expect(bestMember(bySeeders[0], { key: "seeders", dir: "asc" }).indexer).toBe("alpha")
  })

  it("age asc puts the group with the newest member first", () => {
    const groups = sortGroups(groupRows(ROWS), { key: "age", dir: "asc" })
    expect(groups[0].members).toHaveLength(2) // newest member is 2026-01-03
    expect(bestMember(groups[0], { key: "age", dir: "asc" }).indexer).toBe("alpha")
  })
})
