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
 *  1. **infohash** — authoritative for torrents, and torrent-only, so an infohash match
 *     needs no protocol check of its own.
 *  2. **normalized title + EXACT size in bytes, within one protocol.** Size equality is
 *     exact: both sides describe the same file set, and nothing in this codebase shows
 *     the same release's byte count legitimately jittering between trackers. A tolerance
 *     window is what would merge a 1080p encode with a slightly larger sibling, which is
 *     the failure mode that hides a release. A row with no size (absent or 0 — trackers
 *     serve 0 for "unknown") groups only via infohash, and so does a row whose indexer
 *     protocol is unresolved, since usenet and torrent entries must never merge.
 *  3. **Never title alone.**
 */
function groupKey(row: SearchRow): string | null {
  const r = row.release
  if (r.infohash) return `h:${r.infohash.toLowerCase()}`
  if (!r.size || row.protocol === undefined) return null
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
