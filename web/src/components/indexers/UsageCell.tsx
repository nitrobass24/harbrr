import { relativeTime } from "@/lib/format"
import { isIdle } from "@/lib/usage"
import { cn } from "@/lib/utils"
import type { IndexerStats } from "@/lib/api"

// Query count plus last-query age (autobrr/harbrr#487). Never-queried gets its own
// words rather than a "0" that reads like any other number — that case is almost
// always an app that was never pointed at this indexer, not a harbrr problem. Idle
// rows are tinted with the warning colour, the same non-critical attention state
// the expiry cell uses; this never touches the health verdict.
export function UsageCell({ stats }: { stats?: IndexerStats }) {
  // Nothing at all while the stats load: a placeholder glyph here would be a second
  // "…"/"—" in a row that already has one for health and expiry.
  if (!stats) return null
  if (stats.queries === 0) return <span className="text-warn">Never queried</span>

  return (
    <span className={cn("flex flex-col leading-tight", isIdle(stats) ? "text-warn" : "text-muted-foreground")}>
      <span className="tabular-nums">{stats.queries} queries</span>
      <span className="text-[12px]">{stats.lastQueryAt ? relativeTime(stats.lastQueryAt) : "never"}</span>
    </span>
  )
}
