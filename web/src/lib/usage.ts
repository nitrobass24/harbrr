import type { IndexerStats } from "@/lib/api"

// The idle rule (autobrr/harbrr#487), one constant rather than a setting: it only
// decides how a usage cell is tinted and what the dashboard counts, and an indexer
// nothing has queried in a week is worth a look whatever the operator's own cadence
// is. Usage is NOT health — a working indexer nobody queries is idle, not unhealthy.
export const IDLE_AFTER_DAYS = 7
const IDLE_AFTER_MS = IDLE_AFTER_DAYS * 24 * 60 * 60 * 1000

// isIdle: never queried, or nothing for IDLE_AFTER_DAYS. Stats that have not
// loaded yet are not idle — an unknown says nothing either way.
export function isIdle(stats: IndexerStats | undefined, now: Date = new Date()): boolean {
  if (!stats) return false
  if (stats.queries === 0 || !stats.lastQueryAt) return true
  return now.getTime() - new Date(stats.lastQueryAt).getTime() > IDLE_AFTER_MS
}
