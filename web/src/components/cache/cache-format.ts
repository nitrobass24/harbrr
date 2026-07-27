import { relativeTime } from "@/lib/format"

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
