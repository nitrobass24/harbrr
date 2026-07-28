import { Fragment } from "react"

import { BudgetMeter } from "@/components/indexers/BudgetMeter"
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from "@/components/ui/sheet"
import { useIndexerCapabilities, useIndexerStats, useIndexerStatuses } from "@/hooks/useIndexers"
import { relativeTime } from "@/lib/format"
import type { Capabilities, IndexerFailureCounts, IndexerStats } from "@/lib/api"

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
          <h3 className="mb-2 text-[11px] font-medium uppercase tracking-wider text-faint">Recent events</h3>
          {status?.data?.events.length ? (
            <ul className="flex flex-col gap-1.5">
              {status.data.events.slice(0, 10).map((ev, i) => (
                <li key={i} className="flex items-baseline gap-2">
                  <span className="text-bad">{ev.kind}</span>
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
          <h3 className="mb-2 text-[11px] font-medium uppercase tracking-wider text-faint">Capabilities</h3>
          {caps.data ? <CapsSummary caps={caps.data} /> : <p className="text-muted-foreground">Loading…</p>}
        </section>
      </div>
    </>
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
