import type { Instance } from "@/lib/api"

// The per-indexer VIP/membership expiry derivations (autobrr/harbrr#399). Pure, and
// kept out of the component file so the display and the row ordering read the same
// numbers — an "Expired" badge above a row sorted as if it were fine is worse than no
// badge at all.
export type ExpiryState = {
  label: string
  detail: string
  tone: string
  // days until expiry, negative once passed; null when nothing is tracked. Also the
  // sort key (see expirySortKey).
  days: number | null
}

// URGENT_DAYS is when an approaching expiry starts reading as a warning rather than
// as a date. It matches the tightest default notification lead times: by the time
// harbrr has sent the 7-day warning the row should already look different.
const URGENT_DAYS = 7

// daysUntil is the whole-day distance to a YYYY-MM-DD date, both sides taken as bare
// UTC dates so no timezone can shift the count by one. It mirrors the backend's
// daysUntil exactly — the UI must not disagree with the notification.
export function daysUntil(date: string, now: Date = new Date()): number | null {
  const target = Date.parse(`${date}T00:00:00Z`)
  if (Number.isNaN(target)) return null
  // getUTC* — not getFullYear/getMonth/getDate — or an operator west of UTC reads one
  // more day remaining all evening than the backend that sends the notification.
  const today = Date.UTC(now.getUTCFullYear(), now.getUTCMonth(), now.getUTCDate())
  return Math.round((target - today) / 86_400_000)
}

// expiryState derives what to show for one indexer. Deliberately quiet: an untracked
// indexer — the overwhelming majority — gets a dash and nothing else, because this is
// opt-in bookkeeping, not a nag. Only a date that is close or already past earns colour.
export function expiryState(ix: Instance, now?: Date): ExpiryState {
  if (ix.expiryLifetime) {
    return { label: "Lifetime", detail: "never expires", tone: "text-faint", days: null }
  }
  const days = ix.expiresAt ? daysUntil(ix.expiresAt, now) : null
  if (days === null) {
    return { label: "—", detail: "no expiry tracked", tone: "text-faint", days: null }
  }
  const what = ix.expiryKind === "account" ? "Account" : "VIP"
  if (days < 0) {
    // Persistent, not a one-shot: the notification fired once, but the row keeps
    // saying so until the operator renews or clears the date.
    return { label: "Expired", detail: `${what} expired ${ix.expiresAt}`, tone: "text-bad", days }
  }
  if (days === 0) return { label: "Today", detail: `${what} expires today`, tone: "text-bad", days }
  if (days <= URGENT_DAYS) {
    return { label: `${days}d`, detail: `${what} expires in ${days} days (${ix.expiresAt})`, tone: "text-warn", days }
  }
  return { label: ix.expiresAt, detail: `${what} expires in ${days} days`, tone: "text-muted-foreground", days }
}

// expirySortKey orders soonest-first with expired at the very top (negative days are
// the most urgent thing on the page). Untracked and lifetime indexers sink to the
// bottom rather than being dropped — they are not a problem, just not tracked.
export function expirySortKey(ix: Instance, now?: Date): number {
  const { days } = expiryState(ix, now)
  return days ?? Number.POSITIVE_INFINITY
}
