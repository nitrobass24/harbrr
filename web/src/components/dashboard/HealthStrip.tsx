import { Link } from "@tanstack/react-router"
import { HealthCell } from "@/components/indexers/HealthCell"
import { IndexerAvatar } from "@/components/indexers/IndexerAvatar"
import { Button } from "@/components/ui/button"
import { useIndexers, useIndexerStatuses } from "@/hooks/useIndexers"

// Per-indexer health at a glance; shares query keys with the Indexers table.
export function HealthStrip() {
  const indexers = useIndexers()
  const list = indexers.data ?? []
  const statuses = useIndexerStatuses(list.map((ix) => ix.slug))

  if (indexers.isSuccess && list.length === 0) {
    return (
      <div className="grid place-items-center rounded-xl border border-dashed border-border py-16 text-center">
        <div>
          <p className="text-[14px] font-medium">No indexers yet</p>
          <p className="mt-1 text-[13px] text-muted-foreground">Add your first tracker to start searching and syncing.</p>
          <Button className="mt-4" asChild>
            <Link to="/indexers">Add indexer</Link>
          </Button>
        </div>
      </div>
    )
  }

  return (
    <section className="flex flex-col gap-2">
      <h2 className="text-[11px] font-medium uppercase tracking-wider text-faint">Indexer health</h2>
      <div className="flex flex-col rounded-xl border border-border bg-card px-5 py-1 text-[13px]">
        {list.map((ix, i) => (
          <Link
            key={ix.slug}
            to="/indexers"
            className="flex items-center gap-3 border-b border-border/60 py-2.5 transition last:border-b-0 hover:bg-accent/40"
          >
            <IndexerAvatar slug={ix.slug} name={ix.name} />
            <span className="w-44 truncate font-medium">{ix.name}</span>
            {!ix.enabled && <span className="text-[11px] text-faint">disabled</span>}
            <span className="ml-auto">
              <HealthCell status={statuses[i]?.data} />
            </span>
          </Link>
        ))}
      </div>
    </section>
  )
}
