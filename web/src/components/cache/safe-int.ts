// safeInt parses a numeric-input value, keeping the previous value rather than
// clobbering the knob on a transient/empty entry. An empty or whitespace-only
// field (Number("") is 0, not NaN) would otherwise commit 0 — silently disabling
// a knob like refreshAheadPct — and a partial entry like "-" would submit NaN, so
// both fall back to the previous value.
export function safeInt(raw: string, previous: number): number {
  if (raw.trim() === "") return previous
  const n = Number(raw)
  return Number.isNaN(n) ? previous : n
}
