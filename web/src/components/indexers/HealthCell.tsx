import { cn } from "@/lib/utils"
import { relativeTime } from "@/lib/format"
import type { IndexerStatus } from "@/lib/api"

// Operator language for the classified failure kinds, never the raw constant: the
// question a red row has to answer is "what do I go fix", and "auth_failure" is not
// that answer.
const EVENT_LABEL: Record<string, string> = {
  auth_failure: "login failed",
  rate_limited: "rate limited",
  parse_error: "response didn't parse",
  anti_bot: "blocked by anti-bot",
  transport: "couldn't reach the tracker",
}

export function reasonLabel(kind: string): string {
  return EVENT_LABEL[kind] ?? kind
}

// Per-state dot/label treatment. "unknown" (autobrr/harbrr#389, #484) is the
// never-observed state — nothing has ever queried or tested this indexer, so it asserts
// nothing and gets the flat neutral colour and no halo, leaving the eye-catching ones to
// the states that mean something.
const TONE: Record<IndexerStatus["status"], { dot: string; text: string; label: string }> = {
  healthy: { dot: "bg-ok", text: "text-muted-foreground", label: "Healthy" },
  failing: { dot: "bg-bad", text: "text-bad", label: "Failing" },
  unknown: { dot: "bg-faint", text: "text-faint", label: "Never tested" },
}

// healthDetail is the operator-visible read of a status: why it is in this state, when
// the streak started, and when harbrr will try again — all of it already recorded, this
// only surfaces it (autobrr/harbrr#389). Healthy returns nothing: a working indexer
// needs no explanation. Never-tested returns the honest-absence line, never an error.
export function healthDetail(status: IndexerStatus): { label: string; value: string }[] {
  if (status.status === "healthy") return []
  if (status.status === "unknown") {
    return [{ label: "Why", value: "Nothing has queried or tested it yet. Not an error." }]
  }
  const rows: { label: string; value: string }[] = []
  const latest = status.events[0]
  // Failing with no event at all is the derivation's parameter-free rule (#484): searches
  // reached the tracker and none of them succeeded, in a shape nothing classified (a plain
  // 500, or a 200 full of junk). There is no kind and no failing-since to quote, so say
  // exactly that rather than leaving the row blank.
  rows.push({
    label: "Why",
    value: latest ? reasonLabel(latest.kind) : "Searches reach it but none succeed. Nothing classified the failure.",
  })
  // The circuit's streak start when the ladder has been climbed, else the failure the
  // state was derived from — either way, "how long has this been broken".
  const since = status.failingSince ?? latest?.occurred_at
  if (since) rows.push({ label: "Failing since", value: relativeTime(since) })
  if (status.disabledTill) {
    rows.push({ label: "Next attempt", value: new Date(status.disabledTill).toLocaleString() })
  }
  return rows
}

// Health column cell: status dot + label + the latest event, per the mockup. The same
// detail the sheet expands is the cell's hover title, so the table answers "why" without
// a click. status is undefined while the per-slug probe is in flight.
export function HealthCell({ status }: { status?: IndexerStatus }) {
  if (!status) {
    return <span className="text-[13px] text-faint">…</span>
  }

  const tone = TONE[status.status]
  // Only a failing row captions its newest event (autobrr/harbrr#473). Never-tested
  // asserts nothing, so an undated caption there would read "broken now" for a failure
  // that may be a month old; the dated history stays in the expanded detail.
  const latest = status.status === "failing" ? status.events[0] : undefined
  const detail = healthDetail(status).map((d) => `${d.label}: ${d.value}`).join(" · ")

  return (
    <div className="flex items-center gap-2 text-[13px]" title={detail || undefined}>
      <span className="relative flex h-2 w-2">
        {status.status !== "unknown" && (
          <span className={cn("absolute inline-flex h-full w-full rounded-full opacity-60", tone.dot)} />
        )}
        <span className={cn("relative inline-flex h-2 w-2 rounded-full", tone.dot)} />
      </span>
      <span className={tone.text}>{tone.label}</span>
      {latest && (
        <span className="truncate text-[12px] text-faint">
          · {reasonLabel(latest.kind)} {relativeTime(latest.occurred_at)}
        </span>
      )}
    </div>
  )
}
