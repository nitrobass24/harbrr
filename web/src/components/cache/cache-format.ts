import { relativeTime } from "@/lib/format"
import type { CacheStats } from "@/lib/api"

export type StatsWindowKey = "1d" | "7d" | "30d" | "all"

// The selectable stats windows, in the order the API returns them. hours is the
// window's span; 0 marks all-time, which reads the cumulative, restart-persisted
// counters rather than the in-memory hour buckets.
export const STATS_WINDOWS: { key: StatsWindowKey, label: string, hours: number }[] = [
  { key: "1d", label: "24h", hours: 24 },
  { key: "7d", label: "7d", hours: 7 * 24 },
  { key: "30d", label: "30d", hours: 30 * 24 },
  { key: "all", label: "All", hours: 0 },
]

// statsWindow picks one window's figures out of the stats response. Every window
// ships in the one GET, so switching view costs no refetch.
export function statsWindow(stats: CacheStats | undefined, key: StatsWindowKey) {
  return stats?.windows?.find((w) => w.window === key)
}

// coverageNote is the honesty line. The 1d/7d/30d views are IN-MEMORY hour buckets,
// so after a restart (or a stats reset) a "30d" view only reaches back as far as
// windowsSince — without saying so, a tile would silently claim a month of data it
// does not have. Returns null when the window is genuinely fully covered, and for
// "all" (which reads the persisted counters and is never bucket-bound).
export function coverageNote(
  key: StatsWindowKey,
  windowsSince: number | undefined,
  nowMs: number = Date.now()
): string | null {
  const hours = STATS_WINDOWS.find((w) => w.key === key)?.hours ?? 0
  if (!hours || !windowsSince) return null
  if (Math.floor(nowMs / 1000) - windowsSince >= hours * 3600) return null
  // relativeTime directly rather than unixAgo: the caller's clock must reach it.
  const since = relativeTime(new Date(windowsSince * 1000).toISOString(), new Date(nowMs))
  return `in-memory window — only collecting since ${since}`
}

// unixAgo renders one of the cache's nullable Unix-seconds age aggregates. The
// API sends null when the cache is empty, so a falsy value is "—" — never a
// relative time counted from the 1970 epoch.
export function unixAgo(sec: number | null | undefined): string {
  return sec ? relativeTime(new Date(sec * 1000).toISOString()) : "—"
}

// breakerLabel says *when* a tracker's breaker reopens, not just that it is
// open. breakerOpenUntil is a future instant, which relativeTime cannot render
// (it clamps the future to "just now"). A lapsed instant — open until a moment
// already past, the natural half-open window before the next probe reaps it —
// must read as retrying, never as a negative countdown.
export function breakerLabel(untilSec: number, nowMs: number = Date.now()): string {
  const secs = untilSec - Math.floor(nowMs / 1000)
  if (secs <= 0) return "breaker open · retrying"
  return `breaker open · retries in ${secs < 60 ? `${secs}s` : `${Math.ceil(secs / 60)}m`}`
}
