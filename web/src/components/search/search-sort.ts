import type { Instance, Release } from "@/lib/api"

export type SearchRow = {
  release: Release
  indexer: string // slug of the indexer that returned it
  // Acquisition protocol of that indexer, carried down from the indexer list because
  // the search response has no per-release protocol. undefined = unresolved, which
  // grouping treats as "do not merge on anything but an infohash" (search-group.ts).
  protocol?: Instance["protocol"]
}

export type SortKey = "title" | "size" | "seeders" | "age"
export type Sort = { key: SortKey, dir: "asc" | "desc" }

// Negative when `a` sorts before `b`. Exported because group ordering is defined in
// terms of row ordering: a group sorts on the member that sorts first (search-group.ts).
export function compareRows(a: SearchRow, b: SearchRow, sort: Sort): number {
  const factor = sort.dir === "asc" ? 1 : -1
  const ra = a.release
  const rb = b.release
  switch (sort.key) {
    case "title":
      return factor * ra.title.localeCompare(rb.title)
    case "size":
      return factor * ((ra.size ?? 0) - (rb.size ?? 0))
    case "seeders":
      return factor * ((ra.seeders ?? 0) - (rb.seeders ?? 0))
    case "age": {
      const ta = ra.publishDate ? new Date(ra.publishDate).getTime() : 0
      const tb = rb.publishDate ? new Date(rb.publishDate).getTime() : 0
      // "age asc" = newest first (smallest age).
      return -factor * (ta - tb)
    }
  }
}

export function sortRows(rows: SearchRow[], sort: Sort): SearchRow[] {
  return [...rows].sort((a, b) => compareRows(a, b, sort))
}
