import { cn } from "@/lib/utils"
import { relativeTime } from "@/lib/format"
import type { IndexerStatus } from "@/types/api"

const EVENT_LABEL: Record<string, string> = {
  auth_failure: "auth failed",
  rate_limited: "rate limited",
  parse_error: "parse error",
  anti_bot: "anti-bot challenge",
}

// Health column cell: pulsing status dot + label + the latest event, per the
// mockup. status is undefined while the per-slug probe is in flight.
export function HealthCell({ status }: { status?: IndexerStatus }) {
  if (!status) {
    return <span className="text-[13px] text-faint">…</span>
  }

  const healthy = status.status === "healthy"
  const latest = healthy ? undefined : status.events[0]

  return (
    <div className="flex items-center gap-2 text-[13px]">
      <span className="relative flex h-2 w-2">
        <span className={cn("absolute inline-flex h-full w-full rounded-full opacity-60", healthy ? "bg-ok" : "bg-bad")} />
        <span className={cn("relative inline-flex h-2 w-2 rounded-full", healthy ? "bg-ok" : "bg-bad")} />
      </span>
      <span className={healthy ? "text-muted-foreground" : "text-bad"}>
        {healthy ? "Healthy" : "Error"}
      </span>
      {latest && (
        <span className="truncate text-[12px] text-faint">
          · {EVENT_LABEL[latest.kind] ?? latest.kind} {relativeTime(latest.occurred_at)}
        </span>
      )}
    </div>
  )
}
