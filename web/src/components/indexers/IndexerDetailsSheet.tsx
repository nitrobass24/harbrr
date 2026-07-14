import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from "@/components/ui/sheet"
import { useIndexerCapabilities, useIndexerStats, useIndexerStatuses } from "@/hooks/useIndexers"
import { relativeTime } from "@/lib/format"
import type { Capabilities, IndexerFailureCounts } from "@/lib/api"

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
            <dd>{stats.data?.grabs ?? "—"}</dd>
            <dt className="text-muted-foreground">Avg response</dt>
            <dd>{stats.data?.avgResponseMs !== undefined ? `${stats.data.avgResponseMs} ms` : "—"}</dd>
            <dt className="text-muted-foreground">Failures</dt>
            <dd>{totalFailures(stats.data?.failures)}</dd>
            <dt className="text-muted-foreground">Last query</dt>
            <dd>{stats.data?.lastQueryAt ? relativeTime(stats.data.lastQueryAt) : "never"}</dd>
          </dl>
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
