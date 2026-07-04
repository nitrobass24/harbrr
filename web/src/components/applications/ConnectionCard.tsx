import { ListChecks, MoreVertical, Pencil, RefreshCw, Trash2 } from "lucide-react"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger
} from "@/components/ui/dropdown-menu"
import { Switch } from "@/components/ui/switch"
import { hostname, relativeTime, syncStatusClass } from "@/lib/format"
import { cn } from "@/lib/utils"
import type { AppConnection } from "@/types/api"

export type ConnectionActions = {
  onToggle: (id: number, enabled: boolean) => void
  onTest: (id: number) => void
  onSync: (id: number) => void
  onEdit: (conn: AppConnection) => void
  onDelete: (conn: AppConnection) => void
  onStatus: (id: number) => void
  onSelectIndexers: (conn: AppConnection) => void
}

export function ConnectionCard({ conn, syncing, actions }: {
  conn: AppConnection
  syncing?: boolean
  actions: ConnectionActions
}) {
  return (
    <div className="flex items-center gap-4 rounded-xl border border-border bg-card px-5 py-4">
      <div className="flex min-w-0 flex-1 flex-col gap-1">
        <div className="flex items-center gap-2">
          <span className={cn("text-[14px] font-medium", !conn.enabled && "text-muted-foreground")}>{conn.name}</span>
          <Badge variant="secondary" className="px-1.5 py-0 text-[11px] uppercase">{conn.kind}</Badge>
          <Badge variant="outline" className="px-1.5 py-0 text-[11px]">{conn.freeleechMode === "bypass" ? "FL bypass" : "FL honor"}</Badge>
          <Badge variant="outline" className="px-1.5 py-0 text-[11px]">{conn.syncLevel === "full" ? "full sync" : "add/update"}</Badge>
          <Badge variant="outline" className="px-1.5 py-0 text-[11px]">{conn.indexScope === "all" ? "all indexers" : "selected"}</Badge>
        </div>
        <div className="flex items-center gap-2 text-[12px] text-faint">
          <span>{hostname(conn.baseUrl)}</span>
          {conn.lastSyncAt && (
            <span>
              · last sync{" "}
              <span className={syncStatusClass(conn.lastSyncStatus)}>{conn.lastSyncStatus}</span>{" "}
              {relativeTime(conn.lastSyncAt)}
            </span>
          )}
          {conn.lastSyncError && (
            <span className="truncate text-bad" title={conn.lastSyncError}>· {conn.lastSyncError}</span>
          )}
        </div>
      </div>

      <Switch
        aria-label={`${conn.enabled ? "Disable" : "Enable"} ${conn.name}`}
        checked={conn.enabled}
        onCheckedChange={(checked) => actions.onToggle(conn.id, checked)}
      />
      <Button variant="outline" size="sm" onClick={() => actions.onTest(conn.id)}>Test</Button>
      <Button variant="outline" size="sm" disabled={syncing} onClick={() => actions.onSync(conn.id)}>
        <RefreshCw className={cn("h-3.5 w-3.5", syncing && "animate-spin")} />
        {syncing ? "Syncing…" : "Sync now"}
      </Button>
      <DropdownMenu>
        <DropdownMenuTrigger asChild>
          <Button variant="ghost" size="icon" aria-label={`More actions for ${conn.name}`}>
            <MoreVertical className="h-4 w-4" />
          </Button>
        </DropdownMenuTrigger>
        <DropdownMenuContent align="end">
          <DropdownMenuItem onClick={() => actions.onEdit(conn)}>
            <Pencil className="h-4 w-4" /> Edit
          </DropdownMenuItem>
          <DropdownMenuItem onClick={() => actions.onStatus(conn.id)}>
            <ListChecks className="h-4 w-4" /> Sync ledger
          </DropdownMenuItem>
          {conn.indexScope === "selected" && (
            <DropdownMenuItem onClick={() => actions.onSelectIndexers(conn)}>
              <ListChecks className="h-4 w-4" /> Select indexers
            </DropdownMenuItem>
          )}
          <DropdownMenuSeparator />
          <DropdownMenuItem variant="destructive" onClick={() => actions.onDelete(conn)}>
            <Trash2 className="h-4 w-4" /> Delete
          </DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>
    </div>
  )
}
