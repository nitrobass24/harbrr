import { SyncError } from "@/components/applications/SyncError"
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from "@/components/ui/sheet"
import { useConnectionStatus } from "@/hooks/useAppConnections"
import { useIndexers } from "@/hooks/useIndexers"
import { relativeTime, syncStatusClass } from "@/lib/format"
import { cn } from "@/lib/utils"

// Per-indexer sync ledger for one connection: what was pushed where, when,
// and the scrubbed error when a push failed.
export function StatusDrawer({ connectionId, onClose }: { connectionId: number | null, onClose: () => void }) {
  const status = useConnectionStatus(connectionId)
  const indexers = useIndexers()
  const byId = new Map((indexers.data ?? []).map((ix) => [ix.id, ix]))

  return (
    <Sheet open={connectionId !== null} onOpenChange={(open) => { if (!open) onClose() }}>
      <SheetContent side="right" className="w-full overflow-auto sm:max-w-md">
        <SheetHeader>
          <SheetTitle>Sync ledger — {status.data?.name ?? "…"}</SheetTitle>
          <SheetDescription>Every harbrr indexer this connection mirrors.</SheetDescription>
        </SheetHeader>
        <div className="flex flex-col gap-2 px-4 pb-6 text-[13px]">
          {(status.data?.indexers ?? []).map((row) => {
            const ix = byId.get(row.instanceId)
            return (
              <div key={row.instanceId} className="flex flex-col gap-1 border-b border-border/60 pb-2">
                <div className="flex items-baseline gap-2">
                  <span className="font-medium">{ix?.name ?? `#${row.instanceId}`}</span>
                  {!row.selected && <span className="text-[11px] text-faint">not selected</span>}
                  {row.lastPushStatus && (
                    <span className={cn("text-[12px]", syncStatusClass(row.lastPushStatus))}>
                      {row.lastPushStatus}
                    </span>
                  )}
                  <span className="ml-auto shrink-0 text-[12px] text-faint">
                    {row.lastPushedAt ? relativeTime(row.lastPushedAt) : "never pushed"}
                  </span>
                </div>
                {row.lastPushError && <SyncError error={row.lastPushError} />}
              </div>
            )
          })}
          {status.data && status.data.indexers.length === 0 && (
            <p className="text-muted-foreground">Nothing synced yet — run a sync.</p>
          )}
        </div>
      </SheetContent>
    </Sheet>
  )
}
