import { useState } from "react"
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

  const saved = window24h ? cache.data?.hits24h : cache.data?.trackerHitsSaved
  const ratio = window24h ? cache.data?.hitRatio24h : cache.data?.hitRatio

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
        sub={ratio !== undefined ? `${Math.round(ratio * 100)}% hit ratio · ${window24h ? "24h" : "lifetime"}` : undefined}
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
