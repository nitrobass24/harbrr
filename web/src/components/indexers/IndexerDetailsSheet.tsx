import { Fragment } from "react"

import { BudgetMeter } from "@/components/indexers/BudgetMeter"
import { HealthCell, healthDetail, reasonLabel } from "@/components/indexers/HealthCell"
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from "@/components/ui/sheet"
import { useIndexerCapabilities, useIndexerDiagnostics, useIndexerStats, useIndexerStatuses } from "@/hooks/useIndexers"
import { relativeTime } from "@/lib/format"
import type { Capabilities, DiagnosticCapture, IndexerFailureCounts, IndexerStats, IndexerStatus } from "@/lib/api"

// "200 · 40 failed (83%)": grabs alongside the failed attempts and the success rate, the
// number that exposes a tracker which surfaces results reliably but fails on download.
// Falls back to the bare count until something has been attempted, so an untouched
// indexer never reads as 0% (autobrr/harbrr#403).
function grabSummary(stats: IndexerStats | undefined): string {
  if (!stats) return "—"
  if (stats.grabSuccessRate === undefined || stats.grabAttempts === 0) return String(stats.grabs)
  const failed = stats.grabAttempts - stats.grabs
  return `${stats.grabs} · ${failed} failed (${Math.round(stats.grabSuccessRate * 100)}%)`
}

// Per-category tallies: which categories this indexer actually earns its place for.
function CategoryTable({ rows }: { rows: NonNullable<IndexerStats["categories"]> }) {
  return (
    <section>
      <h3 className="mb-2 text-[11px] font-medium uppercase tracking-wider text-faint">By category</h3>
      <dl className="grid grid-cols-[1fr_auto_auto] gap-x-4 gap-y-1.5">
        <dt className="text-[11px] uppercase tracking-wider text-faint">Category</dt>
        <dd className="text-right text-[11px] uppercase tracking-wider text-faint">Results</dd>
        <dd className="text-right text-[11px] uppercase tracking-wider text-faint">Grabs</dd>
        {rows.map((c) => (
          <Fragment key={c.id}>
            <dt className="text-muted-foreground">{c.name}</dt>
            <dd className="text-right tabular-nums">{c.results}</dd>
            <dd className="text-right tabular-nums">{c.grabs}</dd>
          </Fragment>
        ))}
      </dl>
    </section>
  )
}

// Sums the per-kind failure tally into the single count the details sheet displays.
function totalFailures(failures: IndexerFailureCounts | undefined): number {
  if (!failures) return 0
  return failures.authFailure + failures.rateLimited + failures.parseError + failures.antiBot
}

// Right-hand drawer: recent health events, durable stats, and capabilities.
export function IndexerDetailsSheet({ slug, onClose }: { slug: string | null, onClose: () => void }) {
  return (
    <Sheet open={slug !== null} onOpenChange={(open) => { if (!open) onClose() }}>
      <SheetContent side="right" className="w-full overflow-auto sm:max-w-md">
        {slug && <Details slug={slug} />}
      </SheetContent>
    </Sheet>
  )
}

function Details({ slug }: { slug: string }) {
  const [status] = useIndexerStatuses([slug])
  const stats = useIndexerStats(slug)
  const caps = useIndexerCapabilities(slug)
  const diagnostics = useIndexerDiagnostics(slug)

  return (
    <>
      <SheetHeader>
        <SheetTitle>{slug}</SheetTitle>
        <SheetDescription>Status, stats, and capabilities.</SheetDescription>
      </SheetHeader>
      <div className="flex flex-col gap-6 px-4 pb-6 text-[13px]">
        <section>
          <h3 className="mb-2 text-[11px] font-medium uppercase tracking-wider text-faint">Stats</h3>
          <dl className="grid grid-cols-2 gap-x-4 gap-y-1.5">
            <dt className="text-muted-foreground">Queries</dt>
            <dd>{stats.data?.queries ?? "—"}</dd>
            <dt className="text-muted-foreground">Grabs</dt>
            <dd>{grabSummary(stats.data)}</dd>
            <dt className="text-muted-foreground">Avg response</dt>
            <dd>{stats.data?.avgResponseMs !== undefined ? `${stats.data.avgResponseMs} ms` : "—"}</dd>
            <dt className="text-muted-foreground">Failures</dt>
            <dd>{totalFailures(stats.data?.failures)}</dd>
            <dt className="text-muted-foreground">Last query</dt>
            <dd>{stats.data?.lastQueryAt ? relativeTime(stats.data.lastQueryAt) : "never"}</dd>
          </dl>
        </section>

        {stats.data?.categories?.length ? <CategoryTable rows={stats.data.categories} /> : null}

        <section>
          <h3 className="mb-2 text-[11px] font-medium uppercase tracking-wider text-faint">Request budget</h3>
          <BudgetMeter budget={stats.data?.budget} />
        </section>

        <section>
          <h3 className="mb-2 text-[11px] font-medium uppercase tracking-wider text-faint">Health</h3>
          <HealthDetail status={status?.data} />
        </section>

        <section>
          <h3 className="mb-2 text-[11px] font-medium uppercase tracking-wider text-faint">Recent events</h3>
          {status?.data?.events.length ? (
            <ul className="flex flex-col gap-1.5">
              {status.data.events.slice(0, 10).map((ev, i) => (
                <li key={i} className="flex items-baseline gap-2">
                  <span className="text-bad">{reasonLabel(ev.kind)}</span>
                  <span className="truncate text-muted-foreground">{ev.detail}</span>
                  <span className="ml-auto shrink-0 text-[12px] text-faint">{relativeTime(ev.occurred_at)}</span>
                </li>
              ))}
            </ul>
          ) : (
            <p className="text-muted-foreground">No recorded failures.</p>
          )}
        </section>

        <section>
          <h3 className="mb-2 text-[11px] font-medium uppercase tracking-wider text-faint">Diagnostics</h3>
          {diagnostics.data?.captures.length ? (
            <ul className="flex flex-col gap-1.5">
              {diagnostics.data.captures.map((c, i) => <CaptureEntry key={i} capture={c} />)}
            </ul>
          ) : (
            <p className="text-muted-foreground">No captured failures.</p>
          )}
        </section>

        <section>
          <h3 className="mb-2 text-[11px] font-medium uppercase tracking-wider text-faint">Capabilities</h3>
          {caps.data ? <CapsSummary caps={caps.data} /> : <p className="text-muted-foreground">Loading…</p>}
        </section>
      </div>
    </>
  )
}

// The state itself plus what an operator needs to act on it — why, since when, and when
// harbrr tries again (autobrr/harbrr#389). Healthy contributes no rows: the dot is the
// whole story.
function HealthDetail({ status }: { status: IndexerStatus | undefined }) {
  if (!status) return <p className="text-muted-foreground">Loading…</p>
  return (
    <dl className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-1.5">
      <dt className="text-muted-foreground">State</dt>
      <dd><HealthCell status={status} /></dd>
      {healthDetail(status).map((row) => (
        <Fragment key={row.label}>
          <dt className="text-muted-foreground">{row.label}</dt>
          <dd>{row.value}</dd>
        </Fragment>
      ))}
    </dl>
  )
}

// The one line that separates the two failures operators keep conflating: the page
// shape drifted (nothing matched the rows selector) versus a column drifted (a row
// matched but a field inside it did not). Both name the definition node to fix.
function missLine(miss: DiagnosticCapture["selectorMiss"]): string | null {
  if (!miss) return null
  const what = miss.kind === "no_rows" ? "No rows matched" : "Rows matched, field missed"
  return `${what} — ${miss.selector || "(no selector)"} at ${miss.path ?? "?"}`
}

// One captured failed fetch: the headline an operator scans, expandable to the
// redacted request/response harbrr already held. Native <details> — the disclosure
// widget the platform ships, no state and no dependency.
function CaptureEntry({ capture }: { capture: DiagnosticCapture }) {
  const miss = missLine(capture.selectorMiss)
  return (
    <li>
      <details className="rounded border border-border/60 px-2 py-1.5">
        <summary className="cursor-pointer">
          <span className="text-bad">{reasonLabel(capture.kind)}</span>
          {capture.status ? <span className="ml-2 text-muted-foreground">HTTP {capture.status}</span> : null}
          <span className="ml-2 text-[12px] text-faint">{relativeTime(capture.occurred_at)}</span>
        </summary>
        <dl className="mt-2 grid grid-cols-[auto_1fr] gap-x-4 gap-y-1.5">
          <dt className="text-muted-foreground">Request</dt>
          <dd className="break-all">{capture.method} {capture.url}</dd>
          {miss ? (
            <>
              <dt className="text-muted-foreground">Selector</dt>
              <dd className="break-all">{miss}</dd>
            </>
          ) : null}
          {Object.entries(capture.headers ?? {}).sort(([a], [b]) => a.localeCompare(b)).map(([name, value]) => (
            <Fragment key={name}>
              <dt className="text-muted-foreground">{name}</dt>
              <dd className="break-all">{value}</dd>
            </Fragment>
          ))}
        </dl>
        {capture.body ? (
          <pre className="mt-2 max-h-64 overflow-auto whitespace-pre-wrap break-all rounded bg-muted/40 p-2 text-[12px]">
            {capture.body}{capture.bodyTruncated ? "\n… truncated at 64 KiB" : ""}
          </pre>
        ) : null}
      </details>
    </li>
  )
}

function CapsSummary({ caps }: { caps: Capabilities }) {
  const parents = (caps.categories ?? []).filter((c) => c.isParent || !c.parent)
  return (
    <dl className="grid grid-cols-2 gap-x-4 gap-y-1.5">
      <dt className="text-muted-foreground">Search modes</dt>
      <dd>{Object.keys(caps.modes).join(", ") || "—"}</dd>
      <dt className="text-muted-foreground">Categories</dt>
      <dd>{parents.map((c) => c.name).join(", ") || "—"}</dd>
      <dt className="text-muted-foreground">Raw search</dt>
      <dd>{caps.allowRawSearch ? "yes" : "no"}</dd>
    </dl>
  )
}
