import { compareRows, type SearchRow, type Sort } from "@/components/search/search-sort"

/**
 * A distinct release plus every per-tracker row that resolved to it (autobrr/harbrr#398).
 *
 * This is GROUPING, not deduplication: nothing is ever dropped, because which trackers
 * carry a release is the information the operator is picking on (ratio, freeleech,
 * per-tracker seeders). `members` is never empty and keeps the order it came in.
 */
export type ResultGroup = {
  key: string
  members: SearchRow[]
}

// The composite identity the flat list has always keyed rows on.
export function rowKey(row: SearchRow): string {
  const r = row.release
  return `${row.indexer}::${r.link ?? r.magnet ?? r.infohash ?? r.title}`
}

// Titles differ cosmetically between trackers (dots for spaces, casing). Normalization
// is deliberately shallow — nothing that carries meaning (quality, edition, year, group)
// is stripped, because two titles that differ there are two different releases.
function normalizeTitle(title: string): string {
  return title.toLowerCase().replace(/[._]+/g, " ").replace(/\s+/g, " ").trim()
}

/**
 * groupKey is the matcher, in confidence order. null = ungroupable; the row stands
 * alone. Throughout, a false SPLIT is an annoyance and a false MERGE hides a release,
 * so every uncertain case splits.
 *
 *  0. **A resolved protocol is required for any grouping at all**, and is part of every
 *     key. An infohash is not the torrent-only tell it looks like — a newznab feed can
 *     carry one, and hybrid indexers publish it — so without this a usenet row could
 *     merge with its torrent twin, which have no shared identity and different grab
 *     semantics. An unresolved protocol could be either, so it never merges.
 *  1. **infohash** — authoritative within a protocol.
 *  2. **normalized title + EXACT size in bytes.** Size equality is exact: both sides
 *     describe the same file set, and nothing in this codebase shows the same release's
 *     byte count legitimately jittering between trackers. A tolerance window is what
 *     would merge a 1080p encode with a slightly larger sibling, which is the failure
 *     mode that hides a release. A row with no size (absent or 0 — trackers serve 0 for
 *     "unknown") groups only via infohash.
 *  3. **Never title alone.**
 */
function groupKey(row: SearchRow): string | null {
  const r = row.release
  if (row.protocol === undefined) return null
  if (r.infohash) return `h:${row.protocol}:${r.infohash.toLowerCase()}`
  if (!r.size) return null
  return `t:${row.protocol}:${r.size}:${normalizeTitle(r.title)}`
}

// groupRows collapses rows onto their release identity, first-seen order preserved.
export function groupRows(rows: SearchRow[]): ResultGroup[] {
  const groups: ResultGroup[] = []
  const byKey = new Map<string, ResultGroup>()

  rows.forEach((row, i) => {
    const key = groupKey(row)
    const existing = key === null ? undefined : byKey.get(key)
    if (existing) {
      existing.members.push(row)
      return
    }
    const group: ResultGroup = { key: key ?? `solo:${i}:${rowKey(row)}`, members: [row] }
    if (key !== null) byKey.set(key, group)
    groups.push(group)
  })

  return groups
}

// Grouping off: every row is its own group, in the order given. The rendered list is
// then exactly the flat list — same rows, same order — since a one-member group renders
// as today's plain row.
export function soloGroups(rows: SearchRow[]): ResultGroup[] {
  return rows.map((row) => ({ key: rowKey(row), members: [row] }))
}

/**
 * A group sorts — and renders collapsed — on its BEST member under the current sort:
 * the member that would have sorted first in the flat list (highest seeders under
 * "seeders desc", newest under "age asc"). So a group sits exactly where its best member
 * sat, and the row on screen shows the member that earned it that place, rather than
 * sorting by a value the operator cannot see.
 */
export function bestMember(group: ResultGroup, sort: Sort): SearchRow {
  return group.members.reduce((best, row) => compareRows(row, best, sort) < 0 ? row : best)
}

export function sortGroups(groups: ResultGroup[], sort: Sort): ResultGroup[] {
  return [...groups].sort((a, b) => compareRows(bestMember(a, sort), bestMember(b, sort), sort))
}
