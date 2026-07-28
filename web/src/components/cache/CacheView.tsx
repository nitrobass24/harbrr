import { useEffect, useState } from "react"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Switch } from "@/components/ui/switch"
import { STATS_WINDOWS, breakerLabel, coverageNote, statsWindow, unixAgo } from "@/components/cache/cache-format"
import type { StatsWindowKey } from "@/components/cache/cache-format"
import { safeInt } from "@/components/cache/safe-int"
import { LoadError, LoadingBlock } from "@/components/ui/load-error"
import { useCacheConfig, useCacheStats, useFlushCache, useUpdateCacheConfig } from "@/hooks/useSettings"
import { formatSize } from "@/lib/format"
import { notifyError, notifySuccess } from "@/lib/notify"
import { cn } from "@/lib/utils"
import type { CacheConfig } from "@/lib/api"

// Cache observability + the live-tunable knobs, the body of the Cache page.
// trackerHitsSaved is the headline: durable tracker requests answered from
// cache instead of hitting the tracker (the kind-to-trackers value metric).
export function CacheView() {
  const stats = useCacheStats()
  const flush = useFlushCache()
  // Which window the hit tiles read. Every window ships in the one stats GET, so
  // switching is instant — no refetch, no server-side ?window= parameter.
  const [windowKey, setWindowKey] = useState<StatsWindowKey>("all")

  if (stats.isError) return <LoadError what="cache stats" />
  if (stats.isLoading) return <LoadingBlock />

  const view = statsWindow(stats.data, windowKey)
  const coverage = coverageNote(windowKey, stats.data?.windowsSince)

  return (
    <section className="flex flex-col gap-4">
      {stats.data && !stats.data.enabled && (
        <p className="rounded-xl border border-dashed border-border px-5 py-6 text-center text-[13px] text-muted-foreground">
          Caching is disabled — every consumer poll reaches the tracker. Enable it below.
        </p>
      )}

      {stats.data?.enabled && <WindowPicker value={windowKey} onChange={setWindowKey} />}

      {stats.data?.enabled && (
        <div className="grid grid-cols-2 gap-3 lg:grid-cols-4">
          <StatTile label="Tracker hits saved" value={String(view?.hits ?? 0)} highlight />
          <StatTile label="Hit ratio" value={view ? `${Math.round((view.hitRatio ?? 0) * 100)}%` : "—"} />
          <StatTile label="Cached entries" value={String(stats.data.entries ?? 0)} sub={formatSize(stats.data.approxSizeBytes)} />
          <StatTile label="Breaker suppressed" value={String(stats.data.breakerSuppressed ?? 0)} />
        </div>
      )}

      {coverage && <p className="px-1 text-[12px] text-warn">{coverage}</p>}

      {stats.data?.enabled && (
        <p className="px-1 text-[12px] text-faint">
          Oldest entry {unixAgo(stats.data.oldestCachedAt)} · newest {unixAgo(stats.data.newestCachedAt)}
          {" · last served "}{unixAgo(stats.data.lastUsedAt)}
        </p>
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
                  <span className="ml-auto text-bad">{breakerLabel(row.breakerOpenUntil)}</span>
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
            onSuccess: (r) => notifySuccess(`Flushed ${r.flushed} cached entries`),
            onError: (err) => notifyError("Flush failed", err),
          })}
        >
          {flush.isPending ? "Flushing…" : "Flush cache"}
        </Button>
      </div>

      <ConfigForm />
    </section>
  )
}

// WindowPicker is the 24h/7d/30d/All segmented control over the stats tiles. It
// switches locally — every window is already in the response.
function WindowPicker({ value, onChange }: { value: StatsWindowKey, onChange: (k: StatsWindowKey) => void }) {
  return (
    <div className="flex w-fit items-center gap-0.5 rounded-lg border border-border bg-card p-0.5" role="group" aria-label="Stats window">
      {STATS_WINDOWS.map(({ key, label }) => (
        <button
          key={key}
          type="button"
          aria-pressed={value === key}
          onClick={() => onChange(key)}
          className={cn(
            "rounded-md px-2.5 py-1 text-[12px] font-medium text-muted-foreground hover:text-foreground",
            value === key && "bg-accent text-foreground"
          )}
        >
          {label}
        </button>
      ))}
    </div>
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

// help is one plain-language line per knob, matching the tier semantics
// documented in website/docs/features/search-results-cache.md.
const DURATION_KNOBS: { key: keyof CacheConfig, label: string, help: string }[] = [
  { key: "rssTtl", label: "RSS/empty-poll TTL", help: "How long an RSS poll — a what's-new request with no search terms — is remembered." },
  { key: "keywordTtl", label: "Keyword TTL", help: "How long a real search — a title, or an IMDb/TVDB/TMDb ID — is remembered." },
  { key: "thinTtl", label: "Thin-result TTL", help: "Replaces the tier above when a search came back thin. Only ever shortens a TTL." },
  { key: "negativeTtl", label: "Breaker window (0s off)", help: "How long a tracker that just failed is left alone before harbrr retries it." },
  { key: "cleanupInterval", label: "Cleanup interval", help: "How often expired entries are reaped. The cleanup loop re-reads this live." },
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
          onSuccess: () => notifySuccess("Cache config applied (live, no restart)"),
          onError: (err) => notifyError(`Config rejected: ${err.message}`, err),
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
        {DURATION_KNOBS.map(({ key, label, help }) => (
          <Knob key={key} name={key} label={label} help={help}>
            <Input
              id={`knob-${key}`}
              aria-describedby={`knob-${key}-help`}
              className="h-8 font-mono text-[12px]"
              value={String(draft[key])}
              onChange={(e) => setDraft({ ...draft, [key]: e.target.value })}
            />
          </Knob>
        ))}
        <Knob
          name="thinThreshold"
          label="Thin threshold (results)"
          help="How few results count as thin. Raise it if your trackers normally answer with small result sets."
        >
          <Input
            id="knob-thinThreshold"
            aria-describedby="knob-thinThreshold-help"
            className="h-8 font-mono text-[12px]"
            type="number"
            value={draft.thinThreshold}
            onChange={(e) => setDraft({ ...draft, thinThreshold: safeInt(e.target.value, draft.thinThreshold) })}
          />
        </Knob>
        <Knob
          name="refreshAheadPct"
          label="Refresh-ahead % (0 off)"
          help="Past this share of an entry's life, a hit is served instantly and refreshed in the background."
        >
          <Input
            id="knob-refreshAheadPct"
            aria-describedby="knob-refreshAheadPct-help"
            className="h-8 font-mono text-[12px]"
            type="number"
            value={draft.refreshAheadPct}
            onChange={(e) => setDraft({ ...draft, refreshAheadPct: safeInt(e.target.value, draft.refreshAheadPct) })}
          />
        </Knob>
      </div>
      <div>
        <Button type="submit" size="sm" disabled={update.isPending}>
          {update.isPending ? "Applying…" : "Apply"}
        </Button>
      </div>
    </form>
  )
}

// Knob is one labelled config field plus its plain-language help line. The help
// sits under the input (not the label) so the inputs stay on a shared baseline
// across the grid however long the help runs.
function Knob({ name, label, help, children }: { name: string, label: string, help: string, children: React.ReactNode }) {
  return (
    <span className="flex flex-col gap-1.5">
      <Label htmlFor={`knob-${name}`} className="text-[12px]">{label}</Label>
      {children}
      <span id={`knob-${name}-help`} className="text-[11px] leading-snug text-faint">{help}</span>
    </span>
  )
}
