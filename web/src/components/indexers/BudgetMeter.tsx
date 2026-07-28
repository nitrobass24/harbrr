import { cn } from "@/lib/utils"
import type { IndexerBudget, IndexerBudgetKind } from "@/lib/api"

// countdown renders how long is left until an ISO timestamp ("13h", "42m"). Used for
// the budget period boundary, which is always in the future.
export function countdown(iso: string, now: Date = new Date()): string {
  const mins = Math.floor((new Date(iso).getTime() - now.getTime()) / 60_000)
  if (Number.isNaN(mins) || mins < 1) return "under a minute"
  if (mins < 60) return `${mins}m`
  return `${Math.floor(mins / 60)}h`
}

// BudgetMeter shows an indexer's request-budget usage for the current period
// (autobrr/harbrr#402). It only reports counters the registry already keeps.
//
// It stays deliberately quiet: a kind with no cap at all — neither operator-configured
// nor learned from the tracker — has nothing to meter, so it is not drawn, and an
// indexer with no cap of any kind gets a single line instead of an empty gauge. A spent
// budget is styled as a self-imposed guard (warn, not bad): harbrr held the request
// back, the tracker did not fail.
export function BudgetMeter({ budget }: { budget?: IndexerBudget }) {
  if (!budget) return null
  const metered = [
    { label: "Searches", kind: budget.query },
    { label: "Grabs", kind: budget.grab },
  ].filter(({ kind }) => kind.limit > 0 || kind.learned)

  if (metered.length === 0) {
    return <p className="text-muted-foreground">No request budget set — requests are counted, never held back.</p>
  }
  return (
    <div className="flex flex-col gap-3">
      {metered.map(({ label, kind }) => <KindRow key={label} label={label} kind={kind} />)}
      <p className="text-[12px] text-faint">Resets in {countdown(budget.periodEnd)} · every {budget.unit}</p>
    </div>
  )
}

function KindRow({ label, kind }: { label: string, kind: IndexerBudgetKind }) {
  const capped = kind.limit > 0
  // A learned latch has no number behind it (the tracker never declared its cap), so it
  // reads as spent outright; a configured cap is spent once the count reaches it.
  const spent = kind.learned || (capped && kind.used >= kind.limit)
  const pct = capped ? Math.min(100, Math.round((kind.used / kind.limit) * 100)) : 100

  return (
    <div className="flex flex-col gap-1">
      <div className="flex items-baseline gap-2">
        <span className="text-muted-foreground">{label}</span>
        {kind.learned && (
          <span
            className="inline-flex items-center rounded-full border border-warn/40 bg-warn/10 px-2 py-0.5 text-[11px] font-medium text-warn"
            title="harbrr learned this cap from the tracker's own quota error — you did not configure it"
          >
            learned cap
          </span>
        )}
        <span className="ml-auto tabular-nums">
          {capped ? `${kind.used.toLocaleString()} / ${kind.limit.toLocaleString()}` : `${kind.used.toLocaleString()} used`}
        </span>
      </div>
      {/* Decorative: the count beside it carries the same information as text. */}
      <div className="h-1.5 overflow-hidden rounded-full bg-muted" aria-hidden="true">
        <div className={cn("h-full rounded-full", spent ? "bg-warn" : "bg-primary")} style={{ width: `${pct}%` }} />
      </div>
      {spent && (
        <p className="text-[12px] text-warn">
          {kind.learned ? "Tracker reported its quota spent" : "Budget spent for this period"} — harbrr is holding
          requests back until it resets, and answers searches from cache where it can.
        </p>
      )}
    </div>
  )
}
