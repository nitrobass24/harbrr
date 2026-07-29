import { useState } from "react"
import { coverageNote, statsWindow } from "@/components/cache/cache-format"
import { useAppConnections } from "@/hooks/useAppConnections"
import { useIndexers, useIndexerStatuses } from "@/hooks/useIndexers"
import { useCacheStats } from "@/hooks/useSettings"
import { cn } from "@/lib/utils"

// The four headline tiles: indexer health, tracker hits saved (the
// kind-to-trackers value metric), app connections, breaker state.
export function DashboardTiles() {
  const indexers = useIndexers()
  const statuses = useIndexerStatuses((indexers.data ?? []).map((ix) => ix.slug))
  const cache = useCacheStats()
  const connections = useAppConnections()
  // Clicking the cache tile switches between lifetime and rolling-24h stats.
  const [window24h, setWindow24h] = useState(false)

  const total = indexers.data?.length ?? 0
  const healthy = statuses.filter((s) => s.data?.status === "healthy").length
  const failing = statuses.filter((s) => s.data?.status === "failing").length
  const unknown = statuses.filter((s) => s.data?.status === "unknown").length
  const breakerOpen = (cache.data?.byIndexer ?? []).filter((r) => r.breakerOpenUntil).length
  const connected = connections.data?.length ?? 0
  const enabled = (connections.data ?? []).filter((c) => c.enabled).length

  const day = statsWindow(cache.data, "1d")
  const saved = window24h ? day?.hits : cache.data?.trackerHitsSaved
  const ratio = window24h ? day?.hitRatio : cache.data?.hitRatio
  // The 24h view is an in-memory bucket ring, so on a young process it covers less
  // than a day — say so on the tile rather than let "24h" imply a full day.
  const partial = window24h ? coverageNote("1d", cache.data?.windowsSince) : null

  return (
    <div className="grid grid-cols-2 gap-3 lg:grid-cols-4">
      <Tile
        label="Indexers healthy"
        value={`${healthy}/${total}`}
        sub={statusBreakdown(failing, unknown)}
        tone={healthTone(total, healthy, failing)}
      />
      <Tile
        label={window24h ? "Tracker hits saved (24h)" : "Tracker hits saved"}
        value={String(saved ?? 0)}
        sub={cacheTileSub(ratio, window24h, partial)}
        tone="highlight"
        onClick={() => setWindow24h((v) => !v)}
      />
      <Tile label="App connections" value={String(connected)} sub={connected > 0 ? `${enabled} enabled` : undefined} />
      <Tile
        label="Circuit breakers open"
        value={String(breakerOpen)}
        tone={breakerOpen > 0 ? "bad" : undefined}
      />
    </div>
  )
}

// cacheTileSub is the hit-tile subtitle: the ratio and which window it covers, plus
// the coverage caveat when the in-memory 24h ring has not been up a full day.
function cacheTileSub(ratio: number | undefined, window24h: boolean, partial: string | null): string | undefined {
  if (ratio === undefined) return undefined
  const span = window24h ? (partial ? "24h so far" : "24h") : "lifetime"
  return `${Math.round(ratio * 100)}% hit ratio · ${span}`
}

// statusBreakdown spells out the non-healthy remainder of the healthy/total tile, so
// "3/5" says whether the other two are actually broken or merely unheard-from (#389).
function statusBreakdown(failing: number, unknown: number): string | undefined {
  const parts = []
  if (failing > 0) parts.push(`${failing} failing`)
  if (unknown > 0) parts.push(`${unknown} unknown`)
  return parts.length > 0 ? parts.join(" · ") : undefined
}

// healthTone: a known failure is bad, an expired/unheard-from indexer only warns.
function healthTone(total: number, healthy: number, failing: number): "ok" | "warn" | "bad" {
  if (failing > 0) return "bad"
  return total > 0 && healthy < total ? "warn" : "ok"
}

function Tile({ label, value, sub, tone, onClick }: {
  label: string
  value: string
  sub?: string
  tone?: "ok" | "warn" | "bad" | "highlight"
  onClick?: () => void
}) {
  const Wrapper = onClick ? "button" : "div"
  return (
    <Wrapper
      type={onClick ? "button" : undefined}
      onClick={onClick}
      className={cn(
        "flex flex-col gap-0.5 rounded-xl border border-border bg-card px-4 py-3",
        tone === "highlight" && "border-primary/40",
        onClick && "cursor-pointer text-left hover:bg-accent/50"
      )}
    >
      <span className="text-[11px] font-medium uppercase tracking-wider text-faint">{label}</span>
      <span className={cn(
        "text-2xl font-semibold tracking-tight",
        tone === "ok" && "text-ok",
        tone === "warn" && "text-warn",
        tone === "bad" && "text-bad",
        tone === "highlight" && "text-primary"
      )}
      >
        {value}
      </span>
      {sub && <span className="text-[12px] text-faint">{sub}</span>}
    </Wrapper>
  )
}
