import type { Release } from "@/types/api"

export type SearchRow = {
  release: Release
  indexer: string // slug of the indexer that returned it
}

export type SortKey = "title" | "size" | "seeders" | "age"
export type Sort = { key: SortKey, dir: "asc" | "desc" }

export function sortRows(rows: SearchRow[], sort: Sort): SearchRow[] {
  const factor = sort.dir === "asc" ? 1 : -1
  return [...rows].sort((a, b) => {
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
  })
}
