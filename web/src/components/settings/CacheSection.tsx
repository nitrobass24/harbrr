import { useEffect, useState } from "react"
import { toast } from "sonner"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Switch } from "@/components/ui/switch"
import { useCacheConfig, useCacheStats, useFlushCache, useUpdateCacheConfig } from "@/hooks/useSettings"
import { formatSize } from "@/lib/format"
import { cn } from "@/lib/utils"
import type { CacheConfig } from "@/types/api"

// Cache observability + the live-tunable knobs. trackerHitsSaved is the
// headline: durable tracker requests answered from cache instead of hitting
// the tracker (the kind-to-trackers value metric).
export function CacheSection() {
  const stats = useCacheStats()
  const flush = useFlushCache()

  return (
    <section id="cache" className="flex flex-col gap-4">
      <h2 className="text-[14px] font-semibold tracking-tight">Search cache</h2>

      {stats.data && !stats.data.enabled && (
        <p className="rounded-xl border border-dashed border-border px-5 py-6 text-center text-[13px] text-muted-foreground">
          Caching is disabled — every consumer poll reaches the tracker. Enable it below.
        </p>
      )}

      {stats.data?.enabled && (
        <div className="grid grid-cols-2 gap-3 lg:grid-cols-4">
          <StatTile label="Tracker hits saved" value={String(stats.data.trackerHitsSaved ?? 0)} highlight />
          <StatTile label="Hit ratio" value={stats.data.hitRatio !== undefined ? `${Math.round(stats.data.hitRatio * 100)}%` : "—"} />
          <StatTile label="Cached entries" value={String(stats.data.entries ?? 0)} sub={formatSize(stats.data.approxSizeBytes)} />
          <StatTile label="Breaker suppressed" value={String(stats.data.breakerSuppressed ?? 0)} />
        </div>
      )}

      {stats.data?.byIndexer && stats.data.byIndexer.length > 0 && (
        <div className="overflow-hidden rounded-xl border border-border bg-card px-5 py-3 text-[13px]">
          <p className="mb-2 text-[11px] font-medium uppercase tracking-wider text-faint">By indexer</p>
          <div className="flex flex-col gap-1.5">
            {stats.data.byIndexer.map((row) => (
              <div key={row.instanceId} className="flex items-baseline gap-3">
                <span className="w-40 truncate font-medium">{row.name || row.slug || `#${row.instanceId}`}</span>
                <span className="text-muted-foreground">saved {row.hitsSaved ?? 0}</span>
                <span className="text-muted-foreground">
                  ratio {row.hitRatio !== undefined ? `${Math.round(row.hitRatio * 100)}%` : "—"}
                </span>
                <span className="text-faint">{row.entries ?? 0} entries</span>
                {row.breakerOpenUntil ? (
                  <span className="ml-auto text-bad">breaker open</span>
                ) : (
                  <span className="ml-auto text-ok">breaker closed</span>
                )}
              </div>
            ))}
          </div>
        </div>
      )}

      <div>
        <Button
          variant="outline"
          size="sm"
          disabled={flush.isPending || !stats.data?.enabled}
          onClick={() => flush.mutate(undefined, {
            onSuccess: (r) => toast.success(`Flushed ${r.flushed} cached entries`),
            onError: () => toast.error("Flush failed"),
          })}
        >
          {flush.isPending ? "Flushing…" : "Flush cache"}
        </Button>
      </div>

      <ConfigForm />
    </section>
  )
}

function StatTile({ label, value, sub, highlight }: { label: string, value: string, sub?: string, highlight?: boolean }) {
  return (
    <div className={cn("flex flex-col gap-0.5 rounded-xl border border-border bg-card px-4 py-3", highlight && "border-primary/40")}>
      <span className="text-[11px] font-medium uppercase tracking-wider text-faint">{label}</span>
      <span className={cn("text-xl font-semibold tracking-tight", highlight && "text-primary")}>{value}</span>
      {sub && <span className="text-[12px] text-faint">{sub}</span>}
    </div>
  )
}

const DURATION_KNOBS: { key: keyof CacheConfig, label: string }[] = [
  { key: "rssTtl", label: "RSS/empty-poll TTL" },
  { key: "keywordTtl", label: "Keyword TTL" },
  { key: "thinTtl", label: "Thin-result TTL" },
  { key: "negativeTtl", label: "Breaker window (0s off)" },
  { key: "cleanupInterval", label: "Cleanup interval" },
]

// Every knob is runtime-tunable: PUT applies live, no restart.
function ConfigForm() {
  const config = useCacheConfig()
  const update = useUpdateCacheConfig()
  const [draft, setDraft] = useState<CacheConfig | null>(null)

  useEffect(() => {
    if (config.data && draft === null) setDraft(config.data)
  }, [config.data, draft])

  if (!draft) return null

  return (
    <form
      className="flex flex-col gap-3 rounded-xl border border-border bg-card px-5 py-4"
      onSubmit={(e) => {
        e.preventDefault()
        update.mutate(draft, {
          onSuccess: () => toast.success("Cache config applied (live, no restart)"),
          onError: (err) => toast.error(`Config rejected: ${err.message}`),
        })
      }}
    >
      <div className="flex items-center gap-3">
        <p className="text-[11px] font-medium uppercase tracking-wider text-faint">Configuration (applies live)</p>
        <span className="ml-auto flex items-center gap-2 text-[13px]">
          <Label htmlFor="cache-enabled" className="font-normal">Enabled</Label>
          <Switch
            id="cache-enabled"
            checked={draft.enabled}
            onCheckedChange={(checked) => setDraft({ ...draft, enabled: checked })}
          />
        </span>
      </div>
      <div className="grid grid-cols-2 gap-3 lg:grid-cols-3">
        {DURATION_KNOBS.map(({ key, label }) => (
          <span key={key} className="flex flex-col gap-1.5">
            <Label htmlFor={`knob-${key}`} className="text-[12px]">{label}</Label>
            <Input
              id={`knob-${key}`}
              className="h-8 font-mono text-[12px]"
              value={String(draft[key])}
              onChange={(e) => setDraft({ ...draft, [key]: e.target.value })}
            />
          </span>
        ))}
        <span className="flex flex-col gap-1.5">
          <Label htmlFor="knob-thinThreshold" className="text-[12px]">Thin threshold (results)</Label>
          <Input
            id="knob-thinThreshold"
            className="h-8 font-mono text-[12px]"
            type="number"
            value={draft.thinThreshold}
            onChange={(e) => setDraft({ ...draft, thinThreshold: Number(e.target.value) })}
          />
        </span>
        <span className="flex flex-col gap-1.5">
          <Label htmlFor="knob-refreshAheadPct" className="text-[12px]">Refresh-ahead % (0 off)</Label>
          <Input
            id="knob-refreshAheadPct"
            className="h-8 font-mono text-[12px]"
            type="number"
            value={draft.refreshAheadPct}
            onChange={(e) => setDraft({ ...draft, refreshAheadPct: Number(e.target.value) })}
          />
        </span>
      </div>
      <div>
        <Button type="submit" size="sm" disabled={update.isPending}>
          {update.isPending ? "Applying…" : "Apply"}
        </Button>
      </div>
    </form>
  )
}
