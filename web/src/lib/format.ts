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

// formatSize renders a byte count the way trackers do ("2.5 GiB").
export function formatSize(bytes: number | undefined): string {
  if (bytes === undefined || bytes <= 0) return "—"
  const units = ["B", "KiB", "MiB", "GiB", "TiB", "PiB"]
  let value = bytes
  let unit = 0
  while (value >= 1024 && unit < units.length - 1) {
    value /= 1024
    unit++
  }
  return `${unit === 0 ? value : value.toFixed(1)} ${units[unit]}`
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

// KIND_LABELS is display-only — the API/DB slugs (the map's keys) never change.
const KIND_LABELS: Record<string, string> = {
  sonarr: "Sonarr", radarr: "Radarr", lidarr: "Lidarr", readarr: "Readarr", whisparr: "Whisparr",
  qui: "qui", "crossseed-v6": "cross-seed v6",
  qbittorrent: "qBittorrent", blackhole: "Blackhole (watch folder)", sabnzbd: "SABnzbd",
  nzbget: "NZBGet", flood: "Flood", "download-station": "Download Station",
  transmission: "Transmission", deluge: "Deluge", rtorrent: "rTorrent",
}

// kindLabel renders a display name for an app/connection kind slug, falling back to the
// slug itself for anything not in the map.
export function kindLabel(kind: string): string {
  return KIND_LABELS[kind] ?? kind
}

// syncStatusClass maps an app-sync status to its text color. Single source of
// truth for the sync-status styling shared by the connection card, the sync
// report, and the status drawer.
export function syncStatusClass(status: string | undefined): string {
  switch (status) {
    case "ok":
      return "text-ok"
    case "partial":
      return "text-warn"
    case "error":
      return "text-bad"
    case "skipped":
      return "text-faint"
    default:
      return "text-faint"
  }
}
