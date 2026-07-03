// relativeTime renders "2m ago" style timestamps for health events and stats.
export function relativeTime(iso: string, now: Date = new Date()): string {
  const then = new Date(iso).getTime()
  if (Number.isNaN(then)) return ""
  const secs = Math.max(0, Math.floor((now.getTime() - then) / 1000))
  if (secs < 60) return "just now"
  const mins = Math.floor(secs / 60)
  if (mins < 60) return `${mins}m ago`
  const hours = Math.floor(mins / 60)
  if (hours < 24) return `${hours}h ago`
  const days = Math.floor(hours / 24)
  return `${days}d ago`
}

// hostname extracts the display host from a base URL ("" when unparseable).
export function hostname(url: string | undefined): string {
  if (!url) return ""
  try {
    return new URL(url).hostname
  } catch {
    return url
  }
}
