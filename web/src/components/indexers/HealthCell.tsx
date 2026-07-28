import { cn } from "@/lib/utils"
import { relativeTime } from "@/lib/format"
import type { IndexerStatus } from "@/lib/api"

const EVENT_LABEL: Record<string, string> = {
  auth_failure: "auth failed",
  rate_limited: "rate limited",
  parse_error: "parse error",
  anti_bot: "anti-bot challenge",
}

// Per-state dot/label treatment. "unknown" (autobrr/harbrr#389) is the expired or
// never-observed state — it asserts nothing, so it gets the flat neutral colour and
// no halo, leaving the eye-catching ones to the states that mean something.
const TONE: Record<IndexerStatus["status"], { dot: string; text: string; label: string }> = {
  healthy: { dot: "bg-ok", text: "text-muted-foreground", label: "Healthy" },
  failing: { dot: "bg-bad", text: "text-bad", label: "Failing" },
  unknown: { dot: "bg-faint", text: "text-faint", label: "Unknown" },
}

// Health column cell: status dot + label + the latest event, per the mockup. status is
// undefined while the per-slug probe is in flight.
export function HealthCell({ status }: { status?: IndexerStatus }) {
  if (!status) {
    return <span className="text-[13px] text-faint">…</span>
  }

  const tone = TONE[status.status]
  const latest = status.status === "healthy" ? undefined : status.events[0]

  return (
    <div className="flex items-center gap-2 text-[13px]">
      <span className="relative flex h-2 w-2">
        {status.status !== "unknown" && (
          <span className={cn("absolute inline-flex h-full w-full rounded-full opacity-60", tone.dot)} />
        )}
        <span className={cn("relative inline-flex h-2 w-2 rounded-full", tone.dot)} />
      </span>
      <span className={tone.text}>{tone.label}</span>
      {latest && (
        <span className="truncate text-[12px] text-faint">
          · {EVENT_LABEL[latest.kind] ?? latest.kind} {relativeTime(latest.occurred_at)}
        </span>
      )}
    </div>
  )
}
