import { syncStatusClass } from "@/lib/format"
import { cn } from "@/lib/utils"
import type { SyncReport } from "@/types/api"

const ACTION_STYLE: Record<string, string> = {
  created: "text-ok",
  updated: "text-ok",
  deleted: "text-warn",
  failed: "text-bad",
  noop: "text-faint",
}

// Renders one connection's sync report: overall status + the per-indexer
// action ledger (created/updated/noop/deleted/failed with scrubbed errors).
export function SyncReportView({ report }: { report: SyncReport }) {
  return (
    <div className="flex flex-col gap-2 text-[13px]">
      <p>
        Sync status:{" "}
        <span className={cn("font-medium", syncStatusClass(report.status))}>{report.status}</span>
      </p>
      {report.results.length > 0 && (
        <ul className="flex flex-col gap-1">
          {report.results.map((r) => (
            <li key={r.slug} className="flex items-baseline gap-2">
              <span className="font-medium">{r.slug}</span>
              <span className={cn(ACTION_STYLE[r.action] ?? "text-muted-foreground")}>{r.action}</span>
              {r.error && <span className="truncate text-[12px] text-bad" title={r.error}>{r.error}</span>}
            </li>
          ))}
        </ul>
      )}
    </div>
  )
}
